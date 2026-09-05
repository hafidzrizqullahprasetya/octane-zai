# syntax=docker/dockerfile:1.7
ARG GO_IMAGE=golang:1.27-alpine
FROM ${GO_IMAGE} AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/octane-zai ./cmd/octane-zai

FROM alpine:3.21 AS runner
RUN apk --no-cache add ca-certificates tzdata && adduser -D -h /data appuser
COPY --from=builder /out/octane-zai /usr/local/bin/octane-zai
ENV OCTANE_ZAI_DIR=/data \
    PORT=18787 \
    HOSTNAME=0.0.0.0 \
    TZ=Asia/Jakarta
VOLUME /data
EXPOSE 18787
USER appuser
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:18787/healthz | grep -q '"ok":true' || exit 1
ENTRYPOINT ["octane-zai", "serve"]
