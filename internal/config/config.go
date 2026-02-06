package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env                  string
	HTTPAddr             string
	RedisAddr            string
	RedisDB              int
	DatabaseURL          string
	S3Endpoint           string
	S3PublicEndpoint     string
	S3AccessKey          string
	S3SecretKey          string
	S3Bucket             string
	S3Region             string
	S3UsePathStyle       bool
	TempDir              string
	ParserAPIURL         string
	MP3URLTTL            time.Duration
	APIToken             string
	JobRetentionDays     int
	CleanupInterval      time.Duration
	RateLimitPerMinute   int
	MaxJobDuration       time.Duration
	MaxFileSizeBytes     int64
	DownloadConcurrency  int
	TranscodeConcurrency int
	JobTimeout           time.Duration
}

func Load() Config {
	return Config{
		Env:                  getEnv("APP_ENV", "local"),
		HTTPAddr:             getEnv("APP_HTTP_ADDR", ":8080"),
		RedisAddr:            getEnv("REDIS_ADDR", "localhost:6380"),
		RedisDB:              getEnvInt("REDIS_DB", 0),
		DatabaseURL:          getEnv("DATABASE_URL", ""),
		S3Endpoint:           getEnv("S3_ENDPOINT", "http://localhost:9000"),
		S3PublicEndpoint:     getEnv("S3_PUBLIC_ENDPOINT", ""),
		S3AccessKey:          getEnv("S3_ACCESS_KEY", "minio_access"),
		S3SecretKey:          getEnv("S3_SECRET_KEY", "minio_secret"),
		S3Bucket:             getEnv("S3_BUCKET", "v2m"),
		S3Region:             getEnv("S3_REGION", "us-east-1"),
		S3UsePathStyle:       getEnvBool("S3_USE_PATH_STYLE", true),
		TempDir:              getEnv("TEMP_DIR", "./tmp"),
		ParserAPIURL:         getEnv("PARSER_API_URL", "http://localhost:5001"),
		MP3URLTTL:            getEnvDuration("MP3_URL_TTL", 15*time.Minute),
		APIToken:             getEnv("API_TOKEN", ""),
		JobRetentionDays:     getEnvInt("JOB_RETENTION_DAYS", 0),
		CleanupInterval:      getEnvDuration("CLEANUP_INTERVAL", 0),
		RateLimitPerMinute:   getEnvInt("RATE_LIMIT_PER_MIN", 0),
		MaxJobDuration:       getEnvDuration("MAX_JOB_DURATION", 10*time.Minute),
		MaxFileSizeBytes:     int64(getEnvInt("MAX_FILE_SIZE", 200000000)),
		DownloadConcurrency:  getEnvInt("DOWNLOAD_CONCURRENCY", 1),
		TranscodeConcurrency: getEnvInt("TRANSCODE_CONCURRENCY", 1),
		JobTimeout:           getEnvDuration("JOB_TIMEOUT", 10*time.Minute),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return fallback
}
