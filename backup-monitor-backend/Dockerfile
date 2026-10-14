FROM golang:1.27.1-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/api/ ./cmd/api/
COPY cmd/backup-manager/ ./cmd/backup-manager/
COPY internal/ ./internal/
COPY ops/templates.go ops/backup-panel.sh ./ops/
COPY ops/templates/ ./ops/templates/

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /out/api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /out/backup-manager ./cmd/backup-manager

FROM rclone/rclone:1.75.1 AS rclone

# Exportable host tools: the installer copies these to AlmaLinux without giving
# this container a host mount, privileged mode, or the Docker socket.
FROM alpine:3.24 AS host-tools
COPY --from=build /out/backup-manager /backup-manager
COPY --from=rclone /usr/local/bin/rclone /rclone
COPY ops/install-local.sh ops/compose.local.yaml ops/backup-monitor-agent.service ops/backup_monitor_agent.te ops/rclone-COPYING.txt /bundle/
ENTRYPOINT ["/bin/cat", "/bundle/install-local.sh"]

FROM alpine:3.24 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 app \
    && adduser -D -u 10001 -G app app \
    && mkdir /data \
    && chown app:app /data

COPY --from=build /out/api /usr/local/bin/api
WORKDIR /data
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
