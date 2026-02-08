VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)
BINARY   = petalflow
CMD      = ./cmd/petalflow

.PHONY: build test lint vet coverage clean cross snapshot-update

## build: compile the CLI binary
build:
	go build -ldflags='$(LDFLAGS)' -o $(BINARY) $(CMD)

## test: run all tests with race detector
test:
	go test -race ./...

## lint: run golangci-lint
lint:
	golangci-lint run

## vet: run go vet
vet:
	go vet ./...

## coverage: run tests and open coverage report
coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html

## cross: build release binaries for all platforms
cross:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags='$(LDFLAGS)' -o $(BINARY)-linux-amd64     $(CMD)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags='$(LDFLAGS)' -o $(BINARY)-darwin-arm64     $(CMD)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags='$(LDFLAGS)' -o $(BINARY)-windows-amd64.exe $(CMD)

## snapshot-update: regenerate golden test files
snapshot-update:
	go test ./agent/ -run TestCompileSnapshots -update

## clean: remove build artifacts
clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-darwin-arm64 $(BINARY)-windows-amd64.exe
	rm -f coverage.out coverage.html
