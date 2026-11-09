.PHONY: fmt lint test test-integration ext-build up down

fmt:
	gofmt -w .

lint:
	golangci-lint run ./...

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

ext-build:
	npm --prefix extension ci
	npm --prefix extension run build

up:
	docker compose up -d --build

down:
	docker compose down
