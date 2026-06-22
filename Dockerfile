# syntax=docker/dockerfile:1
#
# Runtime-only image. The provider binary is CROSS-COMPILED ON THE HOST by
# `make xpkg.build` (CGO_ENABLED=0, per target arch) and copied in here. There
# is NO in-container `go build`, which is deliberate:
#
#   * Fast: the image build is a plain COPY, not a ~5-min cold compile of the
#     whole dependency tree (the in-container builder had no go-build cache
#     mount and its layer was busted on every source change).
#   * Multi-arch without QEMU: emulation is only needed for RUN steps, and there
#     are none. buildx sets TARGETARCH per --platform; we copy the matching
#     prebuilt binary, so cross-arch images build natively on any host.
#
# Base: gcr.io/distroless/static:nonroot — ships ca-certificates (needed for
# HTTPS to api.timeweb.cloud), tzdata, /etc/passwd, and a nonroot UID (65532).
# This is the standard target for static Go controllers (kubebuilder default),
# replacing the old scratch + manual ca-cert copy.
FROM gcr.io/distroless/static:nonroot

# Provided automatically by buildx per --platform (amd64, arm64, ...).
ARG TARGETARCH

COPY bin/provider-linux-${TARGETARCH} /usr/local/bin/provider

USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/provider"]
