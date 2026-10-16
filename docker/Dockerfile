# Build stage: compile a static binary (CGO disabled, pure-Go dependencies only).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
RUN go vet ./... && go test ./... \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/litefeed-mcp .

# Runtime stage: minimal image with CA certificates for HTTPS feeds.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 litefeed \
    && mkdir -p /data /etc/litefeed && chown litefeed /data
COPY --from=build /out/litefeed-mcp /usr/local/bin/litefeed-mcp
COPY config.example.yaml /etc/litefeed/config.yaml
USER litefeed
WORKDIR /data
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/litefeed-mcp"]
CMD ["-config", "/etc/litefeed/config.yaml"]
