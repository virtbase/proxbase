# syntax=docker/dockerfile:1
# Base images are pinned by digest; Renovate updates them.
FROM --platform=$BUILDPLATFORM golang:1.27-trixie@sha256:2f84bc93ecfb2689f782b153fdcd368b5a7ab96c1386c65cdaccf35e726d6a44 AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/virtbase/proxbase/internal/cli.version=${VERSION}" \
    -o /out/proxbase ./cmd/proxbase

FROM debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f
# Extra apt options for difficult networks, e.g. --build-arg APT_OPTS="-o Acquire::ForceIPv4=true"
ARG APT_OPTS=""
# upgrade: security fixes released after the base image
RUN apt-get update $APT_OPTS \
 && apt-get upgrade -y -o Acquire::Retries=3 $APT_OPTS \
 && apt-get install -y --no-install-recommends -o Acquire::Retries=3 $APT_OPTS qemu-system-x86 qemu-utils ca-certificates openssh-client tini \
 && rm -rf /var/lib/apt/lists/*
# Runs as uid 1000; /dev/kvm access comes from the host (group_add the kvm GID).
RUN useradd --uid 1000 --create-home proxbase && mkdir -p /data && chown proxbase:proxbase /data
COPY --from=build /out/proxbase /usr/local/bin/proxbase
ENV XDG_DATA_HOME=/data XDG_CACHE_HOME=/data/cache PROXBASE_BIND_ADDRESS=0.0.0.0
USER proxbase
VOLUME /data
ENTRYPOINT ["tini", "--", "proxbase"]
CMD ["--help"]
