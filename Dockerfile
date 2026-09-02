FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG VERSION=dev
ARG BUILD_DATE=unknown
ARG BUILD_REVISION=none
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace

COPY src/go.mod src/go.sum ./src/
RUN go -C src mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go -C src build \
  -trimpath \
  -ldflags "-s -w -X github.com/wenisch-tech/cert-manager-webhook-allinkl/internal/version.Version=${VERSION} -X github.com/wenisch-tech/cert-manager-webhook-allinkl/internal/version.Commit=${BUILD_REVISION} -X github.com/wenisch-tech/cert-manager-webhook-allinkl/internal/version.BuildDate=${BUILD_DATE}" \
  -o /out/cert-manager-webhook-allinkl ./cmd/cert-manager-webhook-allinkl

FROM cgr.dev/chainguard/static:latest
ARG VERSION=dev
ARG BUILD_DATE=unknown
ARG BUILD_REVISION=none

LABEL org.opencontainers.image.title="cert-manager-webhook-allinkl" \
      org.opencontainers.image.description="cert-manager ACME DNS-01 solver for All-Inkl (kasserver.com)" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${BUILD_REVISION}" \
      org.opencontainers.image.source="https://github.com/wenisch-tech/cert-manager-webhook-allinkl"

COPY --from=builder /out/cert-manager-webhook-allinkl /usr/local/bin/cert-manager-webhook-allinkl
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/cert-manager-webhook-allinkl"]
