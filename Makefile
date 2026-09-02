APP_NAME := cert-manager-webhook-allinkl
CHART_OCI := ghcr.io/wenisch-tech/helm-charts/cert-manager-webhook-allinkl
PKG := github.com/wenisch-tech/cert-manager-webhook-allinkl
SRC_DIR := src
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell powershell -NoProfile -Command "(Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')")

LDFLAGS := -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT) -X $(PKG)/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build test fmt tidy vet lint-chart publish-artifacthub-metadata

build:
	go -C $(SRC_DIR) build -ldflags "$(LDFLAGS)" -o ../bin/$(APP_NAME) ./cmd/$(APP_NAME)

test:
	go -C $(SRC_DIR) test ./...

vet:
	go -C $(SRC_DIR) vet ./...

fmt:
	gofmt -w ./src/cmd ./src/internal

tidy:
	go -C $(SRC_DIR) mod tidy

lint-chart:
	helm lint charts/$(APP_NAME)

# Publishes artifacthub-repo.yml to the OCI registry under the reserved
# `artifacthub.io` tag. Only needed out-of-band; the release workflow does this
# on every release. Requires `oras` and a prior `oras login ghcr.io`.
publish-artifacthub-metadata:
	oras push $(CHART_OCI):artifacthub.io \
	  --config /dev/null:application/vnd.cncf.artifacthub.config.v1+yaml \
	  artifacthub-repo.yml:application/vnd.cncf.artifacthub.repository-metadata.layer.v1.yaml
