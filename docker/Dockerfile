# syntax=docker/dockerfile:1

# Build stage. It always runs on the build host platform and cross-compiles
# for the target platform: Go needs no emulation for that, which is much
# faster than building under QEMU. CGO is disabled (pure-Go dependencies
# only), so the binary is static.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY src/ ./
# TARGETVARIANT is e.g. "v7" for linux/arm/v7; GOARM expects "7".
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/litefeed-mcp .

# Runtime stage: minimal image with CA certificates for HTTPS feeds.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 litefeed \
    && mkdir -p /data /etc/litefeed && chown litefeed /data
COPY --from=build /out/litefeed-mcp /usr/local/bin/litefeed-mcp
COPY docker/config.yaml /etc/litefeed/config.yaml
USER litefeed
WORKDIR /data
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/litefeed-mcp"]
CMD ["-config", "/etc/litefeed/config.yaml"]
