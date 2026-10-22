# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
# Base images are pinned by digest; Renovate updates them.
FROM --platform=$BUILDPLATFORM golang:1.27-trixie@sha256:fbb7ae40b62ff8afe31662df570d39b4f5052ac32b90dae8e60d7657a20a74f4 AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/virtbase/proxbase/internal/cli.version=${VERSION}" \
    -o /out/proxbase ./cmd/proxbase

FROM debian:trixie-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad
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
