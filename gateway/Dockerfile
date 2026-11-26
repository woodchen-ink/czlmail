# syntax=docker/dockerfile:1.7
# czlmail JMAP gateway. Build context is gateway/.
# The admin web (gateway/web, Next.js static export) gets its own node stage when it lands (#44).

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/gateway .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 gateway \
 && adduser -S -u 10001 -G gateway -h /data gateway \
 && mkdir -p /data && chown gateway:gateway /data
COPY --from=build /out/gateway /usr/local/bin/gateway
USER gateway
ENV GATEWAY_LISTEN=:8080 \
    GATEWAY_DATA_DIR=/data \
    TZ=UTC
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/gateway"]
