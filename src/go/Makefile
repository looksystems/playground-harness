.PHONY: build test test-race vet lint tidy

build:
	go build ./...

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

lint: vet

tidy:
	go mod tidy
