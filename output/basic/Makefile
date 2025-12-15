.PHONY: build test clean docker-build docker-push help

PLUGIN_NAME = out_k8s_splunk.so
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
IMAGE_NAME ?= fluent-bit-k8s-splunk
REGISTRY ?= your-registry

help: ## Show this help
	@echo 'Fluent Bit K8s to Splunk - Basic Version'
	@echo 'Performance: ~500 logs/sec per node'
	@echo ''
	@echo 'Usage: make [target]'
	@echo ''
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the plugin
	@echo "Building basic plugin..."
	go build -buildmode=c-shared -o $(PLUGIN_NAME) .
	@echo "✓ Plugin built: $(PLUGIN_NAME) ($$(du -h $(PLUGIN_NAME) | cut -f1))"

test: ## Run unit tests
	@echo "Running tests..."
	go test -v -race -cover ./...

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -f $(PLUGIN_NAME) *.out *.html
	@echo "✓ Clean complete"

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

docker-build: build ## Build Docker image
	@echo "Building Docker image..."
	docker build -t $(REGISTRY)/$(IMAGE_NAME):basic-$(VERSION) \
		-t $(REGISTRY)/$(IMAGE_NAME):basic-latest \
		-f Dockerfile .
	@echo "✓ Image built: $(REGISTRY)/$(IMAGE_NAME):basic-$(VERSION)"

docker-push: docker-build ## Push Docker image
	@echo "Pushing Docker images..."
	docker push $(REGISTRY)/$(IMAGE_NAME):basic-$(VERSION)
	docker push $(REGISTRY)/$(IMAGE_NAME):basic-latest
	@echo "✓ Images pushed"

all: clean deps build test ## Run all: clean, deps, build, test

.DEFAULT_GOAL := help
