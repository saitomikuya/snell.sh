# syntax=docker/dockerfile:1.7
FROM --platform=$BUILDPLATFORM node:24.7.0-bookworm-slim AS web-build
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:1.26.6-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/internal/api/ui/ ./internal/api/ui/
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/panel ./cmd/panel

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata s6 nftables iptables && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 panel && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin panel
COPY --from=go-build /out/panel /usr/local/bin/panel
RUN ln -s /usr/local/bin/panel /usr/local/bin/panelctl
COPY packaging/docker/entrypoint.sh /usr/local/bin/container-entrypoint
COPY packaging/s6/ /etc/s6/
RUN chmod 0755 /usr/local/bin/container-entrypoint /etc/s6/panel-agent/run /etc/s6/panel-web/run
ENV PANEL_DATA_DIR=/data PANEL_BIND=0.0.0.0 PANEL_PORT=8080 PANEL_UID=10001 PANEL_GID=10001 TZ=Asia/Shanghai
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 --start-period=30s CMD ["/usr/local/bin/panel", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/container-entrypoint"]
CMD ["/usr/bin/s6-svscan", "/etc/s6"]
