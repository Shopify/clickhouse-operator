# ===================
# ===== Builder =====
# ===================

# External images are pinned by digest. To update an image, resolve the
# digest of the new tag and replace the tag and the digest together.
FROM --platform=${BUILDPLATFORM} golang:1.26.11@sha256:4165ee6bf3b7811fd7b40373290405c83d0167dcb87ee5f29c427cdcef45ff8f AS builder

ARG BUILDARCH
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG RELEASE
ARG GCFLAGS

# Install required packages
RUN apt-get update && \
    apt-get install -y apt-utils && \
    apt-get install -y gettext-base wget

# External tools are pinned to a release and verified with SHA-256 before use.
# yq runs on the build host to generate manifests, so it uses BUILDARCH.
# Source: https://github.com/mikefarah/yq/releases/download/v4.54.3/checksums
RUN case "${BUILDARCH}" in \
      amd64) SHA256="129534601871538efe0ba4c5e62a34fb1d5f469bcb975c4fddeb467d82974973" ;; \
      arm64) SHA256="89f69310b61b68d1245065a1241d1fc843f7a36cd6103ffd77ad987613bd1b67" ;; \
      *) echo "Unsupported build architecture: ${BUILDARCH}" >&2; exit 1 ;; \
    esac && \
    wget -O /tmp/yq --progress=bar:force:noscroll "https://github.com/mikefarah/yq/releases/download/v4.54.3/yq_linux_${BUILDARCH}" && \
    echo "${SHA256}  /tmp/yq" | sha256sum -c - && \
    install -m 0755 /tmp/yq /usr/bin/yq && rm /tmp/yq && \
    yq --version

# bash and curl are copied into the runtime image, so they use TARGETARCH.
# bash-static publishes no checksums. The SHA-256 values are from the release
# assets of tag 5.2.015-1.2.3-2.
RUN case "${TARGETARCH}" in \
      amd64) PLATFORM="x86_64";  SHA256="64469a9512a00199c85622ec56f870f97d50457a4e06e0cfa39bae7adf0cc8f2" ;; \
      arm64) PLATFORM="aarch64"; SHA256="8877ad33344af461ed801066322fd9a7808cd73e4e81087da228e32e8fad54ca" ;; \
      *) echo "Unsupported target architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    wget -O /tmp/bash --progress=bar:force:noscroll "https://github.com/robxu9/bash-static/releases/download/5.2.015-1.2.3-2/bash-linux-${PLATFORM}" && \
    echo "${SHA256}  /tmp/bash" | sha256sum -c - && \
    chmod +x /tmp/bash

# static-curl stopped publishing an aarch64 binary after v8.11.0, so arm64
# uses v8.11.0. Source: the GPG-signed sha256sum.txt of each release.
RUN case "${TARGETARCH}" in \
      amd64) URL="https://github.com/moparisthebest/static-curl/releases/download/v8.21.0/curl-amd64"; \
             SHA256="e45ae1805ceb3f816e9768fbbaba05d98a2dc9c5577f66395f37660941d88e19" ;; \
      arm64) URL="https://github.com/moparisthebest/static-curl/releases/download/v8.11.0/curl-aarch64"; \
             SHA256="1b050abd1669f9a2ac29b34eb022cdeafb271dce5a4fb57d8ef8fadff6d7be1f" ;; \
      *) echo "Unsupported target architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    wget -O /tmp/curl --progress=bar:force:noscroll "${URL}" && \
    echo "${SHA256}  /tmp/curl" | sha256sum -c - && \
    chmod +x /tmp/curl

# Reconstruct source tree inside docker
WORKDIR /clickhouse-operator
ADD . .
ENV GCFLAGS="${GCFLAGS}"
ENV GOOS="${TARGETOS}"
ENV GOARCH="${TARGETARCH}"

# Build operator binary with explicitly specified output
RUN OPERATOR_BIN=/tmp/clickhouse-operator bash -xe ./dev/go_build_operator.sh

# ===================
# == Delve builder ==
# ===================
FROM --platform=${BUILDPLATFORM} golang:1.26.11@sha256:4165ee6bf3b7811fd7b40373290405c83d0167dcb87ee5f29c427cdcef45ff8f AS delve-builder
ARG TARGETOS
ARG TARGETARCH
# go install verifies the Delve module with the Go checksum database.
# GOTOOLCHAIN=local keeps the pinned Go toolchain of this image.
# A cross-compiled binary goes to /go/bin/<os>_<arch>/, so move it to /go/bin/.
RUN CGO_ENABLED=0 GO111MODULE=on GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    GOSUMDB=sum.golang.org GOTOOLCHAIN=local \
    go install -ldflags "-s -w -extldflags '-static'" github.com/go-delve/delve/cmd/dlv@v1.27.2 && \
    if [ -f "/go/bin/${TARGETOS}_${TARGETARCH}/dlv" ]; then mv "/go/bin/${TARGETOS}_${TARGETARCH}/dlv" /go/bin/dlv; fi && \
    rm -rf /root/.cache/go-build/ /go/pkg/mod/

# ======================
# ===== Image Base =====
# ======================
FROM gcr.io/distroless/static-debian11:latest-amd64@sha256:e6d589f36c6c7d9a14df69da026b446ac03c0d2027bfca82981b6a1256c2019c AS image-base-amd64
FROM gcr.io/distroless/static-debian11:latest-arm64@sha256:525dfec198b40de63cafe3037a0a54b025de274bc4fb0e3dc39f9457e5485da1 AS image-base-arm64
ARG TARGETARCH
FROM image-base-${TARGETARCH}                       AS image-base

MAINTAINER "Altinity <support@altinity.com>"

LABEL name="ClickHouse operator" \
      maintainer="support@altinity.com" \
      vendor="Altinity" \
      version="${VERSION:-dev}" \
      release="${RELEASE:-1}" \
      summary="ClickHouse operator" \
      description="ClickHouse operator operates ClickHouse clusters in kubernetes"

ADD LICENSE /licenses/

WORKDIR /

# Add config files from local source dir into image
ADD config/config.yaml   /etc/clickhouse-operator/
ADD config/conf.d/*      /etc/clickhouse-operator/conf.d/
ADD config/config.d/*    /etc/clickhouse-operator/config.d/
ADD config/templates.d/* /etc/clickhouse-operator/templates.d/
ADD config/users.d/*     /etc/clickhouse-operator/users.d/

# Copy clickhouse-operator binary into operator image from builder
COPY --from=builder /tmp/clickhouse-operator .
COPY --from=builder /tmp/bash /bin/bash
COPY --from=builder /tmp/bash /bin/sh
COPY --from=builder /tmp/curl /bin/curl

# =======================
# ===== Image Debug =====
# =======================
FROM image-base AS image-debug
RUN echo "Building DEBUG image"
WORKDIR /
COPY --from=delve-builder /go/bin/dlv /go/bin/dlv
CMD ["/go/bin/dlv", "--listen=:40001", "--headless=true", "--api-version=2", "exec", "/clickhouse-operator", "--", "-logtostderr=true", "-v=5"]

# ======================
# ===== Image Prod =====
# ======================
FROM image-base AS image-prod
RUN echo "Building PROD image"
WORKDIR /
USER nobody

# Run /clickhouse-operator -alsologtostderr=true -v=1
# We can specify additional options, such as:
#   --config=/path/to/config
#   --kube-config=/path/to/kubeconf
ENTRYPOINT ["/clickhouse-operator"]
CMD ["-logtostderr=true", "-v=1"]
#CMD ["-alsologtostderr=true", "-v=1"]
