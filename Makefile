# Makefile for rproxy

# Attempt to include the .env file. Use '-' to ignore errors if it doesn't exist.
-include .env

# Export variables from .env to be available to sub-commands
export

# --- Configuration ---
CONTAINER_TOOL ?= podman
IMAGE_NAME     ?= rproxy
IMAGE_TAG      ?= latest
CONTAINER_NAME ?= rproxy-instance

# --- Derived/Hardcoded Settings ---
PODMAN_SSH_HOST    := host.containers.internal
# Attempt to get Podman machine port, fallback to 22
PODMAN_SSH_PORT    := $(shell podman machine inspect --format '{{.SSHConfig.Port}}' 2>/dev/null || echo '22')
# Attempt to get Podman machine key path, fallback to standard user key
PODMAN_MACHINE_KEY := $(shell podman machine inspect --format '{{.SSHConfig.IdentityPath}}' 2>/dev/null || echo '$(HOME)/.ssh/id_rsa')
# Podman Connection (via SSH) - Optional, defaults to 'core'
PODMAN_SSH_USER := core
# SSH Key path inside container (hardcoded in sshclient/client.go)
SSH_KEY_MOUNT_PATH := /ssh/id_rsa
# Named volume for certificates
CERTS_VOLUME_NAME  := rproxy-certs
# Certs path inside container (hardcoded in certs/manager.go)
CERTS_MOUNT_PATH   := /certs
# Optional: Set to true for Let's Encrypt staging/testing (default: false)
LEGO_STAGING := false

# Check required variables from .env are set
REQUIRED_ENV_VARS := GANDI_API_KEY ACME_EMAIL GANDI_ZONE
$(foreach var,$(REQUIRED_ENV_VARS),$(if $(value $(var)),,$(error Please set $(var) in .env))) 
# Check derived key path exists
ifeq ($(wildcard $(PODMAN_MACHINE_KEY)),)
    $(error Cannot find SSH key at derived path: $(PODMAN_MACHINE_KEY). Check 'podman machine inspect' or set PODMAN_MACHINE_KEY in .env manually.)
endif

# --- Targets ---

.PHONY: build run deploy clean help

help: ## Display this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Configured Settings:"
	@echo "  Podman SSH Host:    $(PODMAN_SSH_HOST)"
	@echo "  Podman SSH Port:    $(PODMAN_SSH_PORT)"
	@echo "  Host SSH Key Path:  $(PODMAN_MACHINE_KEY)"
	@echo "  Certs Volume Name:  $(CERTS_VOLUME_NAME)"
	@echo "  Container Tool:     $(CONTAINER_TOOL)"
	@echo "  Image:              $(IMAGE_NAME):$(IMAGE_TAG)"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## Build the container image
	@echo "Building $(IMAGE_NAME):$(IMAGE_TAG) using $(CONTAINER_TOOL)..."
	$(CONTAINER_TOOL) build -t $(IMAGE_NAME):$(IMAGE_TAG) .

run: ## Run the container attached (temporary), uses named cert volume
	@echo "Running $(IMAGE_NAME):$(IMAGE_TAG) container attached..."
	@echo "Using SSH key: $(PODMAN_MACHINE_KEY)"
	@echo "Using certs volume: $(CERTS_VOLUME_NAME) mounted at $(CERTS_MOUNT_PATH)"
	$(CONTAINER_TOOL) run --rm -it \
		--name $(CONTAINER_NAME)-run \
		-p 443:443 \
		-v $(CERTS_VOLUME_NAME):$(CERTS_MOUNT_PATH) \
		-v $(PODMAN_MACHINE_KEY):$(SSH_KEY_MOUNT_PATH):ro \
		-e PODMAN_SSH_USER \
		-e PODMAN_SSH_HOST=$(PODMAN_SSH_HOST) \
		-e PODMAN_SSH_PORT=$(PODMAN_SSH_PORT) \
		-e GANDI_API_KEY \
		-e ACME_EMAIL \
		-e GANDI_ZONE \
		-e LEGO_STAGING \
		$(IMAGE_NAME):$(IMAGE_TAG)

deploy: ## Deploy container detached, uses named cert volume
	@echo "Deploying $(IMAGE_NAME):$(IMAGE_TAG) as $(CONTAINER_NAME) detached..."
	@echo "Using SSH key: $(PODMAN_MACHINE_KEY)"
	@echo "Using certs volume: $(CERTS_VOLUME_NAME) mounted at $(CERTS_MOUNT_PATH)"
	$(CONTAINER_TOOL) run -d \
		--name $(CONTAINER_NAME) \
		--restart unless-stopped \
		-p 443:443 \
		-v $(CERTS_VOLUME_NAME):$(CERTS_MOUNT_PATH) \
		-v $(PODMAN_MACHINE_KEY):$(SSH_KEY_MOUNT_PATH):ro \
		-e PODMAN_SSH_USER \
		-e PODMAN_SSH_HOST=$(PODMAN_SSH_HOST) \
		-e PODMAN_SSH_PORT=$(PODMAN_SSH_PORT) \
		-e GANDI_API_KEY \
		-e ACME_EMAIL \
		-e GANDI_ZONE \
		-e LEGO_STAGING \
		$(IMAGE_NAME):$(IMAGE_TAG)

stop: ## Stop the deployed container
	@echo "Stopping container $(CONTAINER_NAME)..."
	-$(CONTAINER_TOOL) stop $(CONTAINER_NAME)

rm: ## Remove the deployed container
	@echo "Removing container $(CONTAINER_NAME)..."
	-$(CONTAINER_TOOL) rm $(CONTAINER_NAME)

clean: stop rm ## Stop and remove the deployed container
	@echo "Cleaned up container $(CONTAINER_NAME)."

logs: ## View logs of the deployed container
	@echo "Showing logs for $(CONTAINER_NAME)... (Ctrl+C to stop)"
	$(CONTAINER_TOOL) logs -f $(CONTAINER_NAME)

clean-certs-volume: ## Remove the named certificate volume (USE WITH CAUTION)
	@echo "WARNING: This will permanently delete the certificate volume '$(CERTS_VOLUME_NAME)'!"
	@read -p "Are you sure? (y/N) " -n 1 -r; echo
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then \
		 echo "Removing volume $(CERTS_VOLUME_NAME)..."; \
		 -$(CONTAINER_TOOL) volume rm $(CERTS_VOLUME_NAME); \
	else \
		 echo "Volume removal cancelled."; \
	fi

# Default target
all: build
