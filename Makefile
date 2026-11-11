BINARY  := ocis-dump
PKG     := ./cmd/ocis-dump
BIN_DIR := bin
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help

.PHONY: help build test cover cover-html lint hooks fmt tidy docker clean

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the binary into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

test: ## Run tests with the race detector
	go test -race -count=1 ./...

cover: ## Run tests and print coverage per function
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

cover-html: cover ## Open an HTML coverage report
	go tool cover -html=coverage.out

lint: ## Run all pre-commit checks (needs prek and a git repo)
	prek run --all-files

hooks: ## Install git hooks (pre-commit, commit-msg, pre-push)
	prek install --hook-type pre-commit --hook-type commit-msg --hook-type pre-push

fmt: ## Format code (gofmt + goimports via golangci-lint)
	golangci-lint fmt ./...

tidy: ## Tidy go.mod / go.sum
	go mod tidy

docker: ## Build the Docker image
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):$(VERSION) .

clean: ## Remove build and coverage artifacts
	rm -rf $(BIN_DIR) coverage.out
