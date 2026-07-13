# 1. Detect Operating System
ifeq ($(OS),Windows_NT)
    BINARY=supremo.exe
    RM=del /Q /F
    RMDIR=rmdir /Q /S
    # Windows paths use backslashes for certain native shell commands
    TARGET_DIR=test_server
else
    BINARY=supremo
    RM=rm -f
    RMDIR=rm -rf
    TARGET_DIR=test_server
endif

.PHONY: all build run test clean fmt

all: build

build:
	go build -o $(BINARY) cmd/supremo/main.go

run:
	go run cmd/supremo/main.go

test:
	go test -v ./...

fmt:
	go fmt ./...

clean:
	$(RM) $(BINARY)
	@if [ "$(OS)" = "Windows_NT" ]; then \
		if exist $(TARGET_DIR) $(RMDIR) $(TARGET_DIR); \
	else \
		$(RMDIR) $(TARGET_DIR); \
	fi