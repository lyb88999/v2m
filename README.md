# video2mp3 (local dev)

This is a local dev scaffold for the video-to-mp3 SaaS MVP.

## Prereqs
- Docker + Docker Compose
- Go toolchain (for your app)
- ffmpeg (local or inside your app container)

## Start local dependencies

```bash
cp docker.env.example docker.env
docker compose --env-file docker.env up -d
```

`docker.env` is the single source of config for Docker runs (QWEN_* is optional unless you want the AI/UI features).
For local runs, use `local.env`.

Services:
- Redis: `localhost:6380`
- Postgres: `localhost:5432` (db: v2m, user: v2m, pass: v2m_pass)
- MinIO: `http://localhost:9000` (console: `http://localhost:9001`)

## App env (example)

Use these when running the app locally:

```bash
export REDIS_ADDR=localhost:6380
export DATABASE_URL="postgres://v2m:v2m_pass@localhost:5432/v2m?sslmode=disable"
export S3_ENDPOINT="http://localhost:9000"
export S3_PUBLIC_ENDPOINT="http://localhost:9000"
export S3_ACCESS_KEY=minio_access
export S3_SECRET_KEY=minio_secret
export S3_BUCKET=v2m
export S3_REGION=us-east-1
export S3_USE_PATH_STYLE=true
export TEMP_DIR=./tmp
```

Or load the provided file:

```bash
set -a
source local.env
set +a
```

## Frontend (Vite + shadcn)

```bash
cd web
npm install
npm run dev
```

The dev server runs at `http://localhost:5173` and proxies `/jobs` to the API on `http://localhost:8080`.
If you want to call a different API host, set `VITE_API_BASE` before running the dev server.

## Frontend (Docker, production-like)

```bash
make up-full-build
```

This brings up API/Worker/Parser + a static web container on `http://localhost:5173`.

## Batch test platforms

Create a URL list file (one URL per line) and run:

```bash
python3 scripts/test_platforms.py -f scripts/urls.txt
```

It will create jobs, poll status, and print a download URL on success.

## Compose files (split)

- `docker-compose.yml`: infra (redis/postgres/minio)
- `docker-compose.app.yml`: api/worker definitions
- `docker-compose.parser.yml`: video-parser service
- `docker-compose.api.yml` / `docker-compose.worker.yml`: thin wrappers for common runs
- `docker-compose.web.yml`: static web (nginx) + API proxy

## Run API in Docker

```bash
docker compose --env-file docker.env -f docker-compose.yml -f docker-compose.api.yml up -d api
```

## Run worker in Docker

Use an extra compose file to run the worker with `video-parser` + `ffmpeg`:

```bash
docker compose --env-file docker.env -f docker-compose.yml -f docker-compose.worker.yml up -d worker
```

## Run API + Worker in Docker

```bash
docker compose --env-file docker.env -f docker-compose.yml -f docker-compose.api.yml -f docker-compose.worker.yml up -d api worker
```

Or:

```bash
make up-all
```

## Download endpoint

To get an always-fresh signed link, you can hit:

```
GET /jobs/{id}/download
```

This redirects (302) to a short-lived signed MP3 URL.
The signed URL is generated with a `Content-Disposition: attachment` hint so most browsers will download instead of playing.

## Jobs list endpoint

```
GET /jobs?limit=20
```

Returns recent jobs for the frontend list.

## Job events (SSE)

```
GET /jobs/{id}/events
```

Streams job updates via Server-Sent Events. The frontend uses this to avoid polling.

## Auth (optional)

Set `API_TOKEN` to enable auth. Clients should send:

```
Authorization: Bearer <token>
```

Or `X-API-KEY: <token>`.

## CORS (optional)

Set `CORS_ALLOW_ORIGINS` as a comma-separated list of allowed origins.
If you don't have a domain yet, you can temporarily set `*` during early testing.

## Cleanup (optional)

Set `JOB_RETENTION_DAYS` and optionally `CLEANUP_INTERVAL` to enable cleanup.
You can also trigger cleanup manually:

```
POST /admin/cleanup
{ "retention_days": 7 }
```

## Rate limit (optional)

Set `RATE_LIMIT_PER_MIN` to a positive integer to enable a per-IP rate limit
(fixed 1-minute window). Return `429` with `Retry-After` if exceeded.

## Notes
- MinIO bucket is created by `minio-init` on `docker compose up`.
- You can change ports if they conflict with existing services.
- `mp3_url` is a short-lived presigned URL (controlled by `MP3_URL_TTL`) because the bucket remains private.

## Deployment (GHCR + server)

This repo includes a GitHub Actions workflow that builds images and pushes them to GHCR,
then SSH deploys to your server using `docker-compose.prod.yml`.

Server prep (one-time):
1) Install Docker + Docker Compose
2) `git clone https://github.com/lyb88999/v2m.git /opt/v2m`
3) Create `/opt/v2m/docker.env` from `docker.env.example` and fill secrets

GitHub repo secrets needed:
- `SERVER_HOST` / `SERVER_USER` / `SERVER_SSH_KEY`
- `GHCR_USER` / `GHCR_TOKEN` (with read:packages for pulling on server)
- Optional: `PARSER_REPO` to point to your own video-parser fork

For servers that cannot reach Docker Hub, the workflow also mirrors base images
(redis/postgres/minio) into GHCR and the production compose uses those mirrors.
