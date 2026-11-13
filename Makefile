.PHONY: fmt lint test test-integration ext-build up up-demo down logs doctor

COMPOSE = docker compose -f deploy/docker-compose.yml --env-file .env

fmt:
	gofmt -w .

# Same linter and version as CI (golangci-lint via Docker).
lint:
	bash scripts/lint.sh

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

ext-build:
	npm --prefix extension ci
	npm --prefix extension run build

up:
	$(COMPOSE) up -d --build

# Demo profile: short retries and slow frames (deploy/demo.env).
up-demo:
	$(COMPOSE) --env-file deploy/demo.env up -d --build

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f gateway worker

doctor:
	sh scripts/doctor.sh
