.PHONY: help build test vet tidy clean install uninstall \
       docker-build docker-run docker-shell docker-push \
       release-check release-snapshot

# Variables
IMAGE_NAME ?= flux-tools
IMAGE_TAG ?= latest
REGISTRY ?= ghcr.io/alexandervidyaev
FULL_IMAGE := $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build binary locally
	go build -ldflags="-X github.com/alexandervidyaev/flux-tools/internal/cli.version=$(VERSION)" -o flux-tools ./cmd/flux-tools

test: ## Run tests
	go test -v ./...

vet: ## Run go vet
	go vet ./...

tidy: ## Run go mod tidy
	go mod tidy

clean: ## Clean build artifacts
	rm -f flux-tools
	rm -rf dist/
	rm -rf cluster-manifests/

install: build ## Install binary to /usr/local/bin
	sudo cp flux-tools /usr/local/bin/

uninstall: ## Uninstall binary from /usr/local/bin
	sudo rm -f /usr/local/bin/flux-tools

release-check: ## Validate .goreleaser.yaml
	goreleaser check

release-snapshot: ## Build multi-arch binaries into dist/ without publishing
	goreleaser release --snapshot --clean

docker-build: ## Build Docker image
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) .

docker-run: ## Run Docker container
	docker run --rm -it \
		-v $(PWD):/app \
		-v $(HOME)/.flux-tools/cache:/home/flux/.flux-tools/cache \
		$(IMAGE_NAME):$(IMAGE_TAG)

docker-shell: ## Open shell in Docker container
	docker run --rm -it \
		-v $(PWD):/app \
		-v $(HOME)/.flux-tools/cache:/home/flux/.flux-tools/cache \
		--entrypoint /bin/bash \
		$(IMAGE_NAME):$(IMAGE_TAG)

docker-push: ## Push Docker image to registry
	docker tag $(IMAGE_NAME):$(IMAGE_TAG) $(FULL_IMAGE)
	docker push $(FULL_IMAGE)
