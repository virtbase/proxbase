# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/virtbase/proxbase/internal/cli.version=${VERSION}" \
    -o /out/proxbase ./cmd/proxbase

FROM debian:trixie-slim
# Extra apt options for difficult networks, e.g. --build-arg APT_OPTS="-o Acquire::ForceIPv4=true"
ARG APT_OPTS=""
RUN apt-get update $APT_OPTS \
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
