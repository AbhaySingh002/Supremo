.PHONY: all build run test clean fmt

all: build

build:
	go build -o supremo cmd/supremo/main.go

run:
	go run cmd/supremo/main.go

test:
	go test -v ./...

fmt:
	go fmt ./...

clean:
	rm -f supremo
	rm -rf test_server
