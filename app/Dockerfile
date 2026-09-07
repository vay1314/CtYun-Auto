# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.24
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder
ARG TARGETOS TARGETARCH APP_VERSION=dev
WORKDIR /src
RUN apk add --no-cache ca-certificates tzdata
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${APP_VERSION}" -o /out/ctyun-auto ./cmd/ctyun-auto

FROM alpine:3.22
ARG APP_VERSION=dev
LABEL org.opencontainers.image.title="ctyun-auto" \
      org.opencontainers.image.description="CtYun keep-alive, points tasks and management panel" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.source="https://github.com/yin26287903/ctyun-auto"
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 ctyun \
    && adduser -S -D -H -u 10001 -G ctyun ctyun \
    && mkdir -p /app/data /app/static \
    && chown -R ctyun:ctyun /app
WORKDIR /app
COPY --from=builder /out/ctyun-auto /app/ctyun-auto
COPY --chown=ctyun:ctyun app/web/static /app/static
ENV TZ=Asia/Shanghai CTYUN_DATA_DIR=/app/data CTYUN_STATIC_DIR=/app/static APP_PORT=9845
USER ctyun
VOLUME ["/app/data"]
EXPOSE 9845
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -q -T 3 -O /dev/null http://127.0.0.1:9845/health || exit 1
ENTRYPOINT ["/app/ctyun-auto"]
