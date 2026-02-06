package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Store struct {
	db *sql.DB
}

type Job struct {
	ID        string
	SourceURL string
	Platform  string
	Status    string
	Error     sql.NullString
	MP3URL    sql.NullString
	CreatedAt time.Time
	UpdatedAt time.Time
}

func New(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Init(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id UUID PRIMARY KEY,
	source_url TEXT NOT NULL,
	platform TEXT NOT NULL,
	status TEXT NOT NULL,
	error TEXT,
	mp3_url TEXT,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *Store) CreateJob(ctx context.Context, j Job) error {
	const q = `
INSERT INTO jobs (id, source_url, platform, status, error, mp3_url, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
`
	_, err := s.db.ExecContext(ctx, q, j.ID, j.SourceURL, j.Platform, j.Status, nullString(j.Error), nullString(j.MP3URL))
	return err
}

func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	const q = `
SELECT id, source_url, platform, status, error, mp3_url, created_at, updated_at
FROM jobs
WHERE id = $1
`
	var j Job
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&j.ID,
		&j.SourceURL,
		&j.Platform,
		&j.Status,
		&j.Error,
		&j.MP3URL,
		&j.CreatedAt,
		&j.UpdatedAt,
	)
	return j, err
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 20
	}
	const q = `
SELECT id, source_url, platform, status, error, mp3_url, created_at, updated_at
FROM jobs
ORDER BY created_at DESC
LIMIT $1
`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(
			&j.ID,
			&j.SourceURL,
			&j.Platform,
			&j.Status,
			&j.Error,
			&j.MP3URL,
			&j.CreatedAt,
			&j.UpdatedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) ListJobsBefore(ctx context.Context, before time.Time, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 200
	}
	const q = `
SELECT id, source_url, platform, status, error, mp3_url, created_at, updated_at
FROM jobs
WHERE created_at < $1
ORDER BY created_at ASC
LIMIT $2
`
	rows, err := s.db.QueryContext(ctx, q, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(
			&j.ID,
			&j.SourceURL,
			&j.Platform,
			&j.Status,
			&j.Error,
			&j.MP3URL,
			&j.CreatedAt,
			&j.UpdatedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) DeleteJobsBefore(ctx context.Context, before time.Time) (int64, error) {
	const q = `
DELETE FROM jobs
WHERE created_at < $1
`
	res, err := s.db.ExecContext(ctx, q, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) UpdateJobStatus(ctx context.Context, id, status string, errMsg, mp3URL *string) error {
	const q = `
UPDATE jobs
SET status = $2, error = $3, mp3_url = $4, updated_at = NOW()
WHERE id = $1
`
	_, err := s.db.ExecContext(ctx, q, id, status, errMsg, mp3URL)
	return err
}

func nullString(ns sql.NullString) *string {
	if ns.Valid {
		return &ns.String
	}
	return nil
}
