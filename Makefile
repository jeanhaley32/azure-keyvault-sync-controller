# Image configuration
IMAGE_REGISTRY ?= ghcr.io
IMAGE_REPO ?= jeanhaley32/azure-keyvault-sync-controller
IMAGE_TAG ?= latest
IMAGE_NAME = $(IMAGE_REGISTRY)/$(IMAGE_REPO):$(IMAGE_TAG)

# Go configuration
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
BINARY_NAME = azure-keyvault-sync-controller

# Kubernetes configuration
NAMESPACE ?= kube-system

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: build
build: ## Build the controller binary
	@echo "Building $(BINARY_NAME) for $(GOOS)/$(GOARCH)..."
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="-w -s" \
		-o $(BINARY_NAME) \
		.
	@echo "Build complete: $(BINARY_NAME)"

.PHONY: run
run: ## Run the controller locally
	@echo "Running controller..."
	go run .

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -f $(BINARY_NAME)
	@echo "Clean complete"

##@ Docker

.PHONY: docker-build
docker-build: ## Build container image
	@echo "Building Docker image: $(IMAGE_NAME)"
	docker build -t $(IMAGE_NAME) .
	@echo "Build complete: $(IMAGE_NAME)"

.PHONY: docker-push
docker-push: ## Push container image to registry
	@echo "Pushing Docker image: $(IMAGE_NAME)"
	docker push $(IMAGE_NAME)
	@echo "Push complete: $(IMAGE_NAME)"

.PHONY: docker-build-push
docker-build-push: docker-build docker-push ## Build and push container image

##@ Deployment

.PHONY: deploy-rbac
deploy-rbac: ## Deploy RBAC resources
	@echo "Deploying RBAC to namespace $(NAMESPACE)..."
	kubectl apply -f deploy/rbac.yaml
	@echo "RBAC deployed"

.PHONY: deploy-controller
deploy-controller: ## Deploy controller
	@echo "Deploying controller to namespace $(NAMESPACE)..."
	kubectl apply -f deploy/deployment.yaml
	@echo "Controller deployed"

.PHONY: deploy
deploy: deploy-rbac deploy-controller ## Deploy all resources

.PHONY: undeploy
undeploy: ## Remove all deployed resources
	@echo "Removing controller from namespace $(NAMESPACE)..."
	kubectl delete -f deploy/deployment.yaml --ignore-not-found=true
	kubectl delete -f deploy/rbac.yaml --ignore-not-found=true
	@echo "Resources removed"

.PHONY: logs
logs: ## Show controller logs
	kubectl logs -n $(NAMESPACE) -l app=azure-keyvault-sync-controller -f

.PHONY: status
status: ## Check controller status
	kubectl get deployment -n $(NAMESPACE) azure-keyvault-sync-controller
	kubectl get pods -n $(NAMESPACE) -l app=azure-keyvault-sync-controller

##@ Testing

.PHONY: test
test: ## Run tests (placeholder for future)
	@echo "No tests implemented yet"

##@ Utilities

.PHONY: fmt
fmt: ## Run go fmt
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: tidy
tidy: ## Run go mod tidy
	go mod tidy

.PHONY: verify
verify: fmt vet tidy ## Run all verification steps
	@echo "Verification complete"
