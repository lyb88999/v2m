package main

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"video2mp3/internal/config"
	"video2mp3/internal/jobs"
	"video2mp3/internal/queue"
	"video2mp3/internal/storage"
	"video2mp3/internal/store"

	"github.com/hibiken/asynq"
)

func main() {
	cfg := config.Load()

	ctx := context.Background()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("store init: %v", err)
	}
	defer st.Close()
	if err := st.Init(ctx); err != nil {
		log.Fatalf("store schema: %v", err)
	}

	s3, err := storage.NewS3(
		cfg.S3Endpoint,
		cfg.S3AccessKey,
		cfg.S3SecretKey,
		cfg.S3Region,
		cfg.S3Bucket,
		cfg.S3UsePathStyle,
		cfg.S3PublicEndpoint,
	)
	if err != nil {
		log.Fatalf("s3 init: %v", err)
	}

	concurrency := cfg.DownloadConcurrency
	if concurrency < 1 {
		concurrency = 1
	}

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr, DB: cfg.RedisDB},
		asynq.Config{Concurrency: concurrency},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TaskProcessVideo, func(ctx context.Context, t *asynq.Task) error {
		var p queue.ProcessPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return err
		}
		return processJob(ctx, cfg, st, s3, p)
	})

	log.Printf("worker started with concurrency=%d", concurrency)
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker error: %v", err)
	}
}

func processJob(ctx context.Context, cfg config.Config, st *store.Store, s3 *storage.S3Client, p queue.ProcessPayload) error {
	log.Printf("job start id=%s url=%s", p.JobID, p.SourceURL)
	workRoot := strings.TrimSpace(cfg.TempDir)
	if workRoot == "" {
		workRoot = os.TempDir()
	}
	workDir := filepath.Join(workRoot, p.JobID)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return recordFailure(ctx, st, p.JobID, err)
	}
	defer func() {
		_ = os.RemoveAll(workDir)
	}()

	if err := st.UpdateJobStatus(ctx, p.JobID, jobs.StatusDownloading, nil, nil); err != nil {
		return err
	}

	videoPath, err := downloadWithParser(ctx, cfg, workDir, p.SourceURL, p.JobID)
	if err != nil {
		return recordFailure(ctx, st, p.JobID, err)
	}

	if err := st.UpdateJobStatus(ctx, p.JobID, jobs.StatusTranscoding, nil, nil); err != nil {
		return err
	}

	mp3Path := filepath.Join(workDir, p.JobID+".mp3")
	if err := transcodeWithFFmpeg(ctx, videoPath, mp3Path); err != nil {
		return recordFailure(ctx, st, p.JobID, err)
	}

	objectKey := fmt.Sprintf("jobs/%s.mp3", p.JobID)
	mp3Key, err := s3.UploadMP3(ctx, mp3Path, objectKey)
	if err != nil {
		return recordFailure(ctx, st, p.JobID, err)
	}

	if err := st.UpdateJobStatus(ctx, p.JobID, jobs.StatusReady, nil, &mp3Key); err != nil {
		return err
	}
	log.Printf("job done id=%s mp3=%s", p.JobID, mp3Key)
	return nil
}

func transcodeWithFFmpeg(ctx context.Context, inputPath, outputPath string) error {
	cmd := exec.CommandContext(
		ctx,
		"ffmpeg",
		"-hide_banner",
		"-loglevel",
		"error",
		"-y",
		"-i",
		inputPath,
		"-vn",
		"-acodec",
		"libmp3lame",
		"-ar",
		"44100",
		"-b:a",
		"128k",
		outputPath,
	)
	output, err := runCommand(cmd)
	if err != nil {
		if output == "" {
			return fmt.Errorf("ffmpeg failed: %w", err)
		}
		return fmt.Errorf("ffmpeg failed: %w: %s", err, output)
	}
	return nil
}

func runCommand(cmd *exec.Cmd) (string, error) {
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	out = truncate(out, 800)
	return out, err
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

type parserResponse struct {
	Retcode int    `json:"retcode"`
	Retdesc string `json:"retdesc"`
	Data    struct {
		VideoID  string `json:"video_id"`
		Platform string `json:"platform"`
		Title    string `json:"title"`
		VideoURL string `json:"video_url"`
		CoverURL string `json:"cover_url"`
		AudioURL string `json:"audio_url"`
	} `json:"data"`
	Succ bool `json:"succ"`
}

type parserRequest struct {
	Text string `json:"text"`
}

type parserResult struct {
	VideoURL string
	AudioURL string
	Platform string
}

func downloadWithParser(ctx context.Context, cfg config.Config, workDir, sourceURL, jobID string) (string, error) {
	parsed, err := parseWithParser(ctx, cfg, sourceURL)
	if err != nil {
		return "", err
	}

	downloadURL := parsed.VideoURL
	fileExt := ".mp4"
	if strings.TrimSpace(parsed.AudioURL) != "" {
		downloadURL = parsed.AudioURL
		fileExt = ".m4a"
	}
	if strings.TrimSpace(downloadURL) == "" {
		return "", errors.New("parser returned empty media url")
	}

	outPath := filepath.Join(workDir, jobID+fileExt)
	if err := downloadToFile(ctx, downloadURL, outPath, sourceURL, cfg.JobTimeout); err != nil {
		return "", err
	}

	log.Printf("parser resolved platform=%s url=%s", parsed.Platform, downloadURL)
	return outPath, nil
}

func parseWithParser(ctx context.Context, cfg config.Config, sourceURL string) (parserResult, error) {
	baseURL := strings.TrimSpace(cfg.ParserAPIURL)
	if baseURL == "" {
		return parserResult{}, errors.New("PARSER_API_URL is required")
	}
	endpoint, err := url.JoinPath(baseURL, "api/parse")
	if err != nil {
		return parserResult{}, err
	}

	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	gclt, err := randomLetters(32)
	if err != nil {
		return parserResult{}, err
	}
	egct := vigenereEncrypt(gclt, timestampToKey(ts))

	body, err := json.Marshal(parserRequest{Text: sourceURL})
	if err != nil {
		return parserResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return parserResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-GCLT-Text", gclt)
	req.Header.Set("X-EGCT-Text", egct)

	client := &http.Client{Timeout: boundedTimeout(cfg.JobTimeout)}
	resp, err := client.Do(req)
	if err != nil {
		return parserResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return parserResult{}, fmt.Errorf("parser http status %d", resp.StatusCode)
	}

	var parsed parserResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return parserResult{}, err
	}
	if !parsed.Succ || parsed.Retcode != 200 {
		return parserResult{}, fmt.Errorf("parser error: %d %s", parsed.Retcode, parsed.Retdesc)
	}
	if strings.TrimSpace(parsed.Data.VideoURL) == "" && strings.TrimSpace(parsed.Data.AudioURL) == "" {
		return parserResult{}, errors.New("parser returned no media url")
	}

	return parserResult{
		VideoURL: parsed.Data.VideoURL,
		AudioURL: parsed.Data.AudioURL,
		Platform: parsed.Data.Platform,
	}, nil
}

func downloadToFile(ctx context.Context, sourceURL, destPath, referer string, timeout time.Duration) error {
	if strings.TrimSpace(sourceURL) == "" {
		return errors.New("download url is empty")
	}

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := downloadOnce(ctx, sourceURL, destPath, referer, timeout)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableDownload(err) || attempt == maxAttempts {
			return err
		}
		backoff := time.Duration(attempt) * time.Second
		log.Printf("download retrying attempt=%d err=%s", attempt+1, truncate(err.Error(), 200))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return lastErr
}

func boundedTimeout(t time.Duration) time.Duration {
	if t <= 0 {
		return 10 * time.Minute
	}
	return t
}

type downloadError struct {
	err       error
	retryable bool
}

func (e downloadError) Error() string {
	return e.err.Error()
}

func (e downloadError) Unwrap() error {
	return e.err
}

func isRetryableDownload(err error) bool {
	if err == nil {
		return false
	}
	var de downloadError
	if errors.As(err, &de) {
		return de.retryable
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if strings.Contains(err.Error(), "unexpected EOF") {
		return true
	}
	return true
}

func downloadOnce(ctx context.Context, sourceURL, destPath, referer string, timeout time.Duration) error {
	var offset int64
	if fi, err := os.Stat(destPath); err == nil {
		offset = fi.Size()
		if offset < 0 {
			offset = 0
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return downloadError{err: err, retryable: true}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	if strings.TrimSpace(referer) != "" {
		req.Header.Set("Referer", referer)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	client := &http.Client{Timeout: boundedTimeout(timeout)}
	resp, err := client.Do(req)
	if err != nil {
		return downloadError{err: err, retryable: true}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(destPath)
		return downloadError{err: fmt.Errorf("download http status %d", resp.StatusCode), retryable: true}
	default:
		retryable := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
		return downloadError{err: fmt.Errorf("download http status %d", resp.StatusCode), retryable: retryable}
	}

	var f *os.File
	if resp.StatusCode == http.StatusPartialContent && offset > 0 {
		f, err = os.OpenFile(destPath, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return downloadError{err: err, retryable: true}
		}
	} else {
		f, err = os.Create(destPath)
		if err != nil {
			return downloadError{err: err, retryable: true}
		}
	}
	defer func() {
		_ = f.Close()
	}()

	if _, err := io.Copy(f, resp.Body); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "unexpected EOF") {
			return downloadError{err: err, retryable: true}
		}
		return downloadError{err: err, retryable: true}
	}
	return nil
}

func timestampToKey(ts string) string {
	const digitsToLetters = "abcdefghijklmnopqrstuvwxyz"
	var b strings.Builder
	for _, r := range ts {
		if r < '0' || r > '9' {
			b.WriteRune('?')
			continue
		}
		b.WriteByte(digitsToLetters[int(r-'0')])
	}
	return b.String()
}

func vigenereEncrypt(text, key string) string {
	if key == "" {
		return text
	}
	var b strings.Builder
	keyIndex := 0
	keyRunes := []rune(strings.ToLower(key))
	for _, r := range text {
		if !unicode.IsLetter(r) {
			b.WriteRune(r)
			continue
		}
		shiftBase := 'a'
		if unicode.IsUpper(r) {
			shiftBase = 'A'
		}
		keyChar := keyRunes[keyIndex%len(keyRunes)]
		keyShift := keyChar - 'a'
		enc := (r-shiftBase+keyShift)%26 + shiftBase
		b.WriteRune(enc)
		keyIndex++
	}
	return b.String()
}

func randomLetters(n int) (string, error) {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if n <= 0 {
		return "", nil
	}
	var b strings.Builder
	b.Grow(n)
	max := big.NewInt(int64(len(letters)))
	for i := 0; i < n; i++ {
		r, err := crand.Int(crand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(letters[r.Int64()])
	}
	return b.String(), nil
}

func recordFailure(ctx context.Context, st *store.Store, jobID string, err error) error {
	if err == nil {
		return nil
	}
	msg := truncate(err.Error(), 800)
	log.Printf("job failed id=%s err=%s", jobID, msg)
	_ = st.UpdateJobStatus(ctx, jobID, jobs.StatusFailed, &msg, nil)
	if shouldSkipRetry(err) {
		return fmt.Errorf("%w: %s", asynq.SkipRetry, msg)
	}
	return err
}

func shouldSkipRetry(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "parser returned no media url") {
		return true
	}
	if strings.Contains(msg, "parser returned empty media url") {
		return true
	}
	return false
}
