package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"video2mp3/internal/config"
	"video2mp3/internal/jobs"
	"video2mp3/internal/platform"
	"video2mp3/internal/queue"
	"video2mp3/internal/storage"
	"video2mp3/internal/store"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type createJobRequest struct {
	URL string `json:"url"`
}

type createJobResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

type listJobsResponse struct {
	Jobs []jobResponse `json:"jobs"`
}

type jobResponse struct {
	JobID     string  `json:"job_id"`
	SourceURL string  `json:"source_url"`
	Platform  string  `json:"platform"`
	Status    string  `json:"status"`
	Error     *string `json:"error,omitempty"`
	MP3URL    *string `json:"mp3_url,omitempty"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type rateLimitResponse struct {
	Error      string `json:"error"`
	RetryAfter int    `json:"retry_after"`
}

type cleanupRequest struct {
	RetentionDays int `json:"retention_days"`
}

type cleanupResponse struct {
	DeletedJobs    int64 `json:"deleted_jobs"`
	DeletedObjects int   `json:"deleted_objects"`
}

var urlRe = regexp.MustCompile(`https?://\S+`)

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

	client := asynq.NewClient(asynq.RedisClientOpt{Addr: cfg.RedisAddr, DB: cfg.RedisDB})
	defer client.Close()

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

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/admin/cleanup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		retentionDays := cfg.JobRetentionDays
		if r.Body != nil {
			var req cleanupRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.RetentionDays > 0 {
				retentionDays = req.RetentionDays
			}
		}
		if retentionDays <= 0 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "retention_days is required"})
			return
		}
		before := time.Now().AddDate(0, 0, -retentionDays)
		deletedJobs, deletedObjects, err := cleanupJobs(r.Context(), st, s3, cfg, before)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "cleanup failed"})
			return
		}
		writeJSON(w, http.StatusOK, cleanupResponse{
			DeletedJobs:    deletedJobs,
			DeletedObjects: deletedObjects,
		})
	})
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var req createJobRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json"})
				return
			}
			if strings.TrimSpace(req.URL) == "" {
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: "url is required"})
				return
			}
			normalizedURL, ok := extractURL(req.URL)
			if !ok {
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: "no valid url found"})
				return
			}
			plat, ok := platform.Detect(normalizedURL)
			if !ok {
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: "unsupported platform"})
				return
			}

			jobID := uuid.NewString()
			job := store.Job{
				ID:        jobID,
				SourceURL: normalizedURL,
				Platform:  plat,
				Status:    jobs.StatusQueued,
			}
			if err := st.CreateJob(r.Context(), job); err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create job"})
				return
			}

			task, err := queue.NewProcessTask(queue.ProcessPayload{JobID: jobID, SourceURL: normalizedURL})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to enqueue"})
				return
			}
			_, err = client.Enqueue(task, asynq.MaxRetry(3), asynq.Timeout(cfg.JobTimeout))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to enqueue"})
				return
			}

			writeJSON(w, http.StatusAccepted, createJobResponse{JobID: jobID, Status: jobs.StatusQueued})
			return
		case http.MethodGet:
			limit := 20
			if raw := r.URL.Query().Get("limit"); raw != "" {
				if v, err := strconv.Atoi(raw); err == nil {
					limit = v
				}
			}
			if limit <= 0 {
				limit = 20
			}
			if limit > 100 {
				limit = 100
			}
			items, err := st.ListJobs(r.Context(), limit)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load jobs"})
				return
			}
			resp := listJobsResponse{Jobs: make([]jobResponse, 0, len(items))}
			for _, j := range items {
				item, err := buildJobResponse(r.Context(), cfg, s3, j)
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to sign mp3 url"})
					return
				}
				resp.Jobs = append(resp.Jobs, item)
			}
			writeJSON(w, http.StatusOK, resp)
			return
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	})
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/jobs/")
		if strings.HasSuffix(path, "/download") {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			id := strings.TrimSuffix(path, "/download")
			id = strings.TrimSuffix(id, "/")
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
				return
			}
			j, err := st.GetJob(r.Context(), id)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
					return
				}
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load job"})
				return
			}
			if j.Status != jobs.StatusReady {
				writeJSON(w, http.StatusConflict, errorResponse{Error: "job not ready"})
				return
			}
			mp3URL, err := mp3DownloadURLForJob(r.Context(), cfg, s3, j)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to sign mp3 url"})
				return
			}
			if mp3URL == nil {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "mp3 not found"})
				return
			}
			http.Redirect(w, r, *mp3URL, http.StatusFound)
			return
		}
		if strings.HasSuffix(path, "/events") {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			id := strings.TrimSuffix(path, "/events")
			id = strings.TrimSuffix(id, "/")
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
				return
			}
			streamJobEvents(w, r, st, s3, cfg, id)
			return
		}
		if strings.HasSuffix(path, "/retry") {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			id := strings.TrimSuffix(path, "/retry")
			id = strings.TrimSuffix(id, "/")
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
				return
			}
			j, err := st.GetJob(r.Context(), id)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
					return
				}
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load job"})
				return
			}
			if j.Status != jobs.StatusFailed && j.Status != jobs.StatusExpired {
				writeJSON(w, http.StatusConflict, errorResponse{Error: "job not retryable"})
				return
			}
			if err := st.UpdateJobStatus(r.Context(), j.ID, jobs.StatusQueued, nil, nil); err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update job"})
				return
			}
			task, err := queue.NewProcessTask(queue.ProcessPayload{JobID: j.ID, SourceURL: j.SourceURL})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to enqueue"})
				return
			}
			if _, err := client.Enqueue(task, asynq.MaxRetry(3), asynq.Timeout(cfg.JobTimeout)); err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to enqueue"})
				return
			}
			writeJSON(w, http.StatusAccepted, createJobResponse{JobID: j.ID, Status: jobs.StatusQueued})
			return
		}

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		id := path
		if id == "" {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
			return
		}
		j, err := st.GetJob(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load job"})
			return
		}
		resp, err := buildJobResponse(r.Context(), cfg, s3, j)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to sign mp3 url"})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	handler := corsMiddleware(cfg.CORSAllowOrigins, rateLimitMiddleware(cfg.RateLimitPerMinute, time.Minute, authMiddleware(cfg.APIToken, mux)))

	if cfg.CleanupInterval > 0 && cfg.JobRetentionDays > 0 {
		go func() {
			ticker := time.NewTicker(cfg.CleanupInterval)
			defer ticker.Stop()
			for range ticker.C {
				before := time.Now().AddDate(0, 0, -cfg.JobRetentionDays)
				if _, _, err := cleanupJobs(context.Background(), st, s3, cfg, before); err != nil {
					log.Printf("cleanup failed: %v", err)
				}
			}
		}()
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("api listening on %s", cfg.HTTPAddr)
	log.Fatal(srv.ListenAndServe())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w http.ResponseWriter, event string, data []byte) error {
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if len(data) > 0 {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "data: {}\n\n"); err != nil {
			return err
		}
	}
	return nil
}

func nullStringPtr(ns sql.NullString) *string {
	if ns.Valid {
		return &ns.String
	}
	return nil
}

func buildJobResponse(ctx context.Context, cfg config.Config, s3 *storage.S3Client, j store.Job) (jobResponse, error) {
	mp3URL, err := mp3URLForJob(ctx, cfg, s3, j)
	if err != nil {
		return jobResponse{}, err
	}
	return jobResponse{
		JobID:     j.ID,
		SourceURL: j.SourceURL,
		Platform:  j.Platform,
		Status:    j.Status,
		Error:     nullStringPtr(j.Error),
		MP3URL:    mp3URL,
		CreatedAt: j.CreatedAt.In(time.Local).Format(time.RFC3339),
		UpdatedAt: j.UpdatedAt.In(time.Local).Format(time.RFC3339),
	}, nil
}

func extractURL(input string) (string, bool) {
	match := urlRe.FindString(strings.TrimSpace(input))
	if match == "" {
		return "", false
	}
	match = strings.TrimRight(match, ".,;:!?)\"'")
	return match, true
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func objectKeyFromURL(raw, bucket string) (string, bool) {
	if strings.TrimSpace(bucket) == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	path := strings.TrimPrefix(u.Path, "/")
	if strings.HasPrefix(u.Host, bucket+".") {
		if path == "" {
			return "", false
		}
		return path, true
	}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 2 && parts[0] == bucket {
		return parts[1], true
	}
	return "", false
}

func mp3URLForJob(ctx context.Context, cfg config.Config, s3 *storage.S3Client, j store.Job) (*string, error) {
	if !j.MP3URL.Valid || strings.TrimSpace(j.MP3URL.String) == "" {
		return nil, nil
	}
	raw := strings.TrimSpace(j.MP3URL.String)
	key := raw
	if isHTTPURL(raw) {
		if parsedKey, ok := objectKeyFromURL(raw, cfg.S3Bucket); ok {
			key = parsedKey
		} else {
			return &raw, nil
		}
	}
	signed, err := s3.PresignMP3(ctx, key, cfg.MP3URLTTL)
	if err != nil {
		return nil, err
	}
	return &signed, nil
}

func mp3DownloadURLForJob(ctx context.Context, cfg config.Config, s3 *storage.S3Client, j store.Job) (*string, error) {
	if !j.MP3URL.Valid || strings.TrimSpace(j.MP3URL.String) == "" {
		return nil, nil
	}
	raw := strings.TrimSpace(j.MP3URL.String)
	key := raw
	if isHTTPURL(raw) {
		if parsedKey, ok := objectKeyFromURL(raw, cfg.S3Bucket); ok {
			key = parsedKey
		} else {
			return &raw, nil
		}
	}
	filename := fmt.Sprintf("video2mp3-%s.mp3", j.ID)
	signed, err := s3.PresignMP3Download(ctx, key, cfg.MP3URLTTL, filename)
	if err != nil {
		return nil, err
	}
	return &signed, nil
}

func authMiddleware(token string, next http.Handler) http.Handler {
	if strings.TrimSpace(token) == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !isAuthorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAuthorized(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") && strings.TrimPrefix(auth, "Bearer ") == token {
		return true
	}
	if r.Header.Get("X-API-KEY") == token {
		return true
	}
	if r.URL != nil {
		if q := r.URL.Query().Get("token"); q != "" && q == token {
			return true
		}
	}
	return false
}

func corsMiddleware(allowOrigins string, next http.Handler) http.Handler {
	if strings.TrimSpace(allowOrigins) == "" {
		return next
	}

	allowAll := false
	allowed := map[string]struct{}{}
	for _, part := range strings.Split(allowOrigins, ",") {
		origin := strings.TrimSpace(part)
		if origin == "" {
			continue
		}
		if origin == "*" {
			allowAll = true
			continue
		}
		allowed[origin] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowAll || containsOrigin(allowed, origin)) {
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-API-KEY")
			w.Header().Set("Access-Control-Expose-Headers", "Retry-After,X-RateLimit-Limit,X-RateLimit-Remaining")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func containsOrigin(allowed map[string]struct{}, origin string) bool {
	if len(allowed) == 0 {
		return false
	}
	_, ok := allowed[origin]
	return ok
}

func streamJobEvents(w http.ResponseWriter, r *http.Request, st *store.Store, s3 *storage.S3Client, cfg config.Config, id string) {
	j, err := st.GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load job"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "stream unsupported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	resp, err := buildJobResponse(r.Context(), cfg, s3, j)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to sign mp3 url"})
		return
	}
	if payload, err := json.Marshal(resp); err == nil {
		_ = writeSSE(w, "", payload)
		flusher.Flush()
	}

	if j.Status == jobs.StatusReady || j.Status == jobs.StatusFailed || j.Status == jobs.StatusExpired {
		return
	}

	lastUpdated := j.UpdatedAt
	ticker := time.NewTicker(3 * time.Second)
	keepalive := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-ticker.C:
			next, err := st.GetJob(r.Context(), id)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return
				}
				continue
			}
			if !next.UpdatedAt.After(lastUpdated) && next.Status == j.Status {
				continue
			}
			lastUpdated = next.UpdatedAt
			j = next
			resp, err := buildJobResponse(r.Context(), cfg, s3, j)
			if err != nil {
				continue
			}
			if payload, err := json.Marshal(resp); err == nil {
				_ = writeSSE(w, "", payload)
				flusher.Flush()
			}
			if j.Status == jobs.StatusReady || j.Status == jobs.StatusFailed || j.Status == jobs.StatusExpired {
				return
			}
		}
	}
}

type rateLimiter struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	entries     map[string]*rateEntry
	lastCleanup time.Time
}

type rateEntry struct {
	count int
	reset time.Time
}

func rateLimitMiddleware(limit int, window time.Duration, next http.Handler) http.Handler {
	if limit <= 0 || window <= 0 {
		return next
	}
	limiter := &rateLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string]*rateEntry),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/events") {
			next.ServeHTTP(w, r)
			return
		}
		key := clientIP(r)
		allowed, retryAfter, remaining := limiter.allow(key)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
		if remaining >= 0 {
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		}
		if !allowed {
			seconds := int(retryAfter.Round(time.Second).Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeJSON(w, http.StatusTooManyRequests, rateLimitResponse{
				Error:      "rate limit exceeded",
				RetryAfter: seconds,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *rateLimiter) allow(key string) (bool, time.Duration, int) {
	if rl == nil || rl.limit <= 0 || rl.window <= 0 {
		return true, 0, -1
	}
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry := rl.entries[key]
	if entry == nil || now.After(entry.reset) {
		entry = &rateEntry{count: 0, reset: now.Add(rl.window)}
		rl.entries[key] = entry
	}

	if entry.count >= rl.limit {
		rl.cleanupLocked(now)
		retryAfter := time.Until(entry.reset)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return false, retryAfter, 0
	}

	entry.count++
	remaining := rl.limit - entry.count
	rl.cleanupLocked(now)
	return true, 0, remaining
}

func (rl *rateLimiter) cleanupLocked(now time.Time) {
	if now.Sub(rl.lastCleanup) < rl.window {
		return
	}
	for key, entry := range rl.entries {
		if now.After(entry.reset) {
			delete(rl.entries, key)
		}
	}
	rl.lastCleanup = now
}

func clientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func cleanupJobs(ctx context.Context, st *store.Store, s3 *storage.S3Client, cfg config.Config, before time.Time) (int64, int, error) {
	var deletedObjects int
	for {
		items, err := st.ListJobsBefore(ctx, before, 200)
		if err != nil {
			return 0, deletedObjects, err
		}
		if len(items) == 0 {
			break
		}
		for _, j := range items {
			key := objectKeyFromJob(cfg, j)
			if key != "" {
				if err := s3.DeleteObject(ctx, key); err == nil {
					deletedObjects++
				}
			}
		}
		if len(items) < 200 {
			break
		}
	}
	deletedJobs, err := st.DeleteJobsBefore(ctx, before)
	if err != nil {
		return 0, deletedObjects, err
	}
	return deletedJobs, deletedObjects, nil
}

func objectKeyFromJob(cfg config.Config, j store.Job) string {
	if !j.MP3URL.Valid || strings.TrimSpace(j.MP3URL.String) == "" {
		return ""
	}
	raw := strings.TrimSpace(j.MP3URL.String)
	if isHTTPURL(raw) {
		if parsedKey, ok := objectKeyFromURL(raw, cfg.S3Bucket); ok {
			return parsedKey
		}
		return ""
	}
	return raw
}
