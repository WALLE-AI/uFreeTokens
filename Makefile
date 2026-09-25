.PHONY: build test test-race vet lint run-gateway run-admin run-worker migrate-up migrate-down deps-up deps-down tidy

MODULE := github.com/WALLE-AI/uFreeTokens
GOOSE_DIR := migrations
DSN ?= postgres://uft:uft@localhost:5432/uft?sslmode=disable

build:
	go build ./...

test:
	go test ./... -count=1

# 需要 cgo + C 工具链（Windows 上需装 mingw-w64 等），CI 环境建议跑这个。
test-race:
	go test ./... -race -count=1

vet:
	go vet ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

run-gateway:
	go run ./cmd/gateway -config config/gateway.example.yaml

run-admin:
	go run ./cmd/admin -config config/gateway.example.yaml

run-worker:
	UFT_CONFIG=config/gateway.example.yaml go run ./cmd/worker

deps-up:
	docker compose -f deploy/docker-compose.yml up -d

deps-down:
	docker compose -f deploy/docker-compose.yml down

migrate-up:
	goose -dir $(GOOSE_DIR) postgres "$(DSN)" up

migrate-down:
	goose -dir $(GOOSE_DIR) postgres "$(DSN)" down
