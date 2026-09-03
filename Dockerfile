# syntax=docker/dockerfile:1

# Custom management panel image (single-file SPA built from
# https://github.com/qingdeng888/Cli-Proxy-API-Management-Center).
# Override the source with BuildKit named context, e.g.:
#   docker build --build-context web=context:../Cli-Proxy-API-Management-Center
ARG WEB_IMAGE=ghcr.io/qingdeng888/cli-proxy-web:latest
FROM ${WEB_IMAGE} AS web

FROM golang:1.26-bookworm AS builder

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends build-essential git && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./

RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=1 GOOS=linux go build -buildvcs=false -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" -o ./CLIProxyAPI ./cmd/server/

FROM debian:bookworm

RUN apt-get update && apt-get install -y --no-install-recommends tzdata ca-certificates curl && rm -rf /var/lib/apt/lists/*

RUN mkdir /CLIProxyAPI

COPY --from=builder ./app/CLIProxyAPI /CLIProxyAPI/CLIProxyAPI

COPY config.example.yaml /CLIProxyAPI/config.example.yaml

# Bundle the custom management panel (single-file SPA from the web image) as
# the default management.html. A mounted ./static volume still overrides it,
# and MANAGEMENT_STATIC_PATH + disable-auto-update-panel keep it from being
# replaced by the upstream panel at runtime.
COPY --from=web /usr/share/nginx/html/index.html /CLIProxyAPI/static/management.html

WORKDIR /CLIProxyAPI

EXPOSE 8317

ENV TZ=Asia/Shanghai

RUN cp /usr/share/zoneinfo/${TZ} /etc/localtime && echo "${TZ}" > /etc/timezone

# Serve the bundled panel and disable remote panel auto-update.
ENV MANAGEMENT_STATIC_PATH=/CLIProxyAPI/static

# 健康检查端口可用环境变量 HEALTHCHECK_PORT 覆盖（默认 8317，与 EXPOSE 对齐）。
# 若 config.yaml 里 port 不同，请在 compose/运行时同步设置 HEALTHCHECK_PORT。
ENV HEALTHCHECK_PORT=8317
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD curl -fsS "http://127.0.0.1:${HEALTHCHECK_PORT}/healthz" || exit 1

CMD ["./CLIProxyAPI"]
