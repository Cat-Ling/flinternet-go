# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH
ARG TARGETARM
ARG VERSION=1.0.0

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web

RUN set -eu; \
    export CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH:-amd64}"; \
    if [ "${GOARCH}" = "arm" ] && [ -n "${TARGETARM}" ]; then \
      export GOARM="${TARGETARM#v}"; \
    fi; \
    go build -trimpath -ldflags="-s -w -X github.com/flinternet/flinternet/internal/server.Version=${VERSION}" -o /out/flinternet ./cmd/flinternet

FROM alpine:3.21
LABEL org.opencontainers.image.title="Flinternet" \
      org.opencontainers.image.description="Privacy-respecting, unauthenticated open-source Pinterest client" \
      org.opencontainers.image.source="https://github.com/Cat-Ling/flinternet-go" \
      org.opencontainers.image.licenses="GPL-3.0"

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/flinternet /usr/local/bin/flinternet

EXPOSE 8080
USER nobody:nobody
ENTRYPOINT ["/usr/local/bin/flinternet"]
