.PHONY: build build-all test lint

VERSION ?= dev

build:
	go build -ldflags="-X main.version=$(VERSION)" -o cortex-proxy .

build-all:
	GOOS=darwin  GOARCH=amd64  go build -ldflags="-X main.version=$(VERSION)" -o dist/cortex-proxy-darwin-amd64 .
	GOOS=darwin  GOARCH=arm64  go build -ldflags="-X main.version=$(VERSION)" -o dist/cortex-proxy-darwin-arm64 .
	GOOS=linux   GOARCH=amd64  go build -ldflags="-X main.version=$(VERSION)" -o dist/cortex-proxy-linux-amd64 .
	GOOS=windows GOARCH=amd64  go build -ldflags="-X main.version=$(VERSION)" -o dist/cortex-proxy-windows-amd64.exe .

test:
	go test ./... -v -race

lint:
	go vet ./...
