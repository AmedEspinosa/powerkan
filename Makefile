APP_NAME := powerkan

.PHONY: build
build:
	go build -o $(APP_NAME) ./cmd/powerkan

.PHONY: test
test:
	go test ./...

.PHONY: fmt
fmt:
	go fmt ./...
