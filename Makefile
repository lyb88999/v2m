SHELL := /bin/sh
ENV_FILE ?= docker.env

.PHONY: up down ps logs worker-docker api-docker web-docker up-all up-all-build up-full up-full-build build-api build-worker build-parser build-web build-all

up:
	docker compose --env-file $(ENV_FILE) up -d

down:
	docker compose --env-file $(ENV_FILE) down

ps:
	docker compose --env-file $(ENV_FILE) ps

logs:
	docker compose --env-file $(ENV_FILE) logs -f

worker-docker:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.worker.yml up -d worker

api-docker:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.api.yml up -d api

web-docker:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.api.yml -f docker-compose.web.yml up -d web

up-all:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.api.yml -f docker-compose.worker.yml up -d api worker

up-all-build:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.api.yml -f docker-compose.worker.yml up -d --build

up-full:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.api.yml -f docker-compose.worker.yml -f docker-compose.web.yml up -d

up-full-build:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.api.yml -f docker-compose.worker.yml -f docker-compose.web.yml up -d --build

build-api:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.api.yml build api

build-worker:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.worker.yml build worker

build-parser:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.worker.yml build video-parser

build-web:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.api.yml -f docker-compose.web.yml build web

build-all:
	docker compose --env-file $(ENV_FILE) -f docker-compose.yml -f docker-compose.api.yml -f docker-compose.worker.yml build
