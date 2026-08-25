# 1. Detect Operating System
ifeq ($(OS),Windows_NT)
    BINARY=supremo.exe
    RM=del /Q /F
    RMDIR=rmdir /Q /S
    TARGET_DIR=test_server
else
    BINARY=supremo
    RM=rm -f
    RMDIR=rm -rf
    TARGET_DIR=test_server
endif

VERSION ?= dev

.PHONY: all build dev release run run-dev test test-debug test-race test-race-debug bench fmt vet lint precommit clean

all: build

build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) cmd/supremo/main.go

dev:
	go build -tags debug -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BINARY) cmd/supremo/main.go

release:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) cmd/supremo/main.go

run:
	go run cmd/supremo/main.go

run-dev:
	go run -tags debug cmd/supremo/main.go --debug

test:
	go test -v ./...

test-debug:
	go test -tags debug -v ./...

test-race:
	go test -race ./...

test-race-debug:
	go test -tags debug -race ./...

bench:
	go test -bench=. -benchmem -run=^$ ./internal/ui/...

fmt:
	go fmt ./...

vet:
	go vet ./...
	go vet -tags debug ./...

lint: vet
	git diff --check

precommit: fmt vet
	go test -race ./...
	go test -tags debug -race ./...
	go build ./cmd/supremo
	go build -tags debug ./cmd/supremo

clean:
	$(RM) $(BINARY)
	$(RMDIR) $(TARGET_DIR)
	$(RMDIR) .supremo-dev
