# Multi-stage build for the Timeweb Crossplane provider.
# Stage 1: build the static binary with the host Go toolchain.
# Stage 2: copy into a minimal scratch image (no shell, no libc).

FROM golang:1.26-alpine AS builder

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Cache Go module downloads as a separate layer.
COPY go.mod go.sum ./
RUN go mod download

COPY apis/ apis/
COPY cmd/ cmd/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
      -ldflags="-s -w -X github.com/lebedevdsl/crossplane-provider-timeweb/internal/version.Version=${VERSION}" \
      -o /workspace/provider \
      ./cmd/provider

FROM scratch AS runtime

# CA bundle for outbound HTTPS to api.timeweb.cloud.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# /tmp is conventional for some Kubernetes patterns (downward API write, etc.).
COPY --from=builder /tmp /tmp

COPY --from=builder /workspace/provider /usr/local/bin/provider

USER 65532:65532

EXPOSE 8080 8081

ENTRYPOINT ["/usr/local/bin/provider"]
