BINARY := bin/api
MODULE_DIR := backend

.PHONY: run build test tidy fmt vet clean

## run: start the API and serve the frontend on :8080
run:
	cd $(MODULE_DIR) && go run ./cmd/api -frontend ../frontend

## build: compile the API binary into bin/
build:
	cd $(MODULE_DIR) && go build -o ../$(BINARY) ./cmd/api

## test: run the Go test suite
test:
	cd $(MODULE_DIR) && go test ./...

## tidy: sync go.mod/go.sum
tidy:
	cd $(MODULE_DIR) && go mod tidy

## fmt: format Go sources
fmt:
	cd $(MODULE_DIR) && gofmt -w .

## vet: run go vet
vet:
	cd $(MODULE_DIR) && go vet ./...

## clean: remove build artifacts
clean:
	rm -rf bin
