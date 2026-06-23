# Makefile for video_converter ExApp
# Usage:
#   make build          — build Docker image locally
#   make push           — push to registry (requires REGISTRY variable)
#   make build-push     — build + push
#   make dev            — run locally with docker-compose (manual/dev mode)
#
# Examples:
#   make build REGISTRY=ghcr.io/kyasu-404
#   make push  REGISTRY=ghcr.io/kyasu-404

APP_ID      := video_converter_exapp
APP_VERSION := 1.0.9
REGISTRY    ?= ghcr.io/kyasu-404
IMAGE_NAME  := $(REGISTRY)/nextcloud-video-converter
IMAGE_TAG   ?= latest

.PHONY: build push build-push dev clean

build:
	docker build \
		--platform linux/amd64 \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		-t $(IMAGE_NAME):$(APP_VERSION) \
		.

push:
	docker push $(IMAGE_NAME):$(IMAGE_TAG)
	docker push $(IMAGE_NAME):$(APP_VERSION)

build-push: build push

# Load image into local Docker daemon without pushing to remote registry.
# Useful for testing with a local HaRP that has access to the Docker socket.
load:
	docker build \
		--platform linux/amd64 \
		--load \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		.

# Run in dev mode (manual-install, registers UI at startup via OCS API)
dev:
	docker compose up --build

clean:
	docker compose down -v
	docker rmi $(IMAGE_NAME):$(IMAGE_TAG) $(IMAGE_NAME):$(APP_VERSION) 2>/dev/null || true
