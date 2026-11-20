# Run from the Dashboard directory with --target backend, frontend or host-tools.
FROM golang:1.27.1-alpine AS backend-build
WORKDIR /src
COPY backup-monitor-backend/go.mod backup-monitor-backend/go.sum ./
RUN go mod download
COPY backup-monitor-backend/*.go ./
COPY backup-monitor-backend/backup-manager/ ./backup-manager/
COPY backup-monitor-backend/backupops/ ./backupops/
COPY backup-monitor-backend/hostagent/ ./hostagent/
COPY backup-monitor-backend/ops/templates.go backup-monitor-backend/ops/backup-panel.sh ./ops/
COPY backup-monitor-backend/ops/templates/ ./ops/templates/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api . \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/backup-manager ./backup-manager

FROM rclone/rclone:1.75.1 AS rclone

FROM alpine:3.24 AS host-tools
COPY --from=backend-build /out/backup-manager /backup-manager
COPY --from=rclone /usr/local/bin/rclone /rclone
COPY backup-monitor-backend/ops/install-local.sh backup-monitor-backend/ops/compose.local.yaml backup-monitor-backend/ops/backup-monitor-agent.service backup-monitor-backend/ops/backup_monitor_agent.te backup-monitor-backend/ops/rclone-COPYING.txt /bundle/
COPY backup-monitor-backend/ops/install-restore-check.sh backup-monitor-backend/ops/backup-monitor-restore-check.service backup-monitor-backend/ops/backup-monitor-restore-check.timer /bundle/
ENTRYPOINT ["/bin/cat", "/bundle/install-local.sh"]

FROM alpine:3.24 AS backend
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 app \
    && adduser -D -u 10001 -G app app \
    && mkdir /data \
    && chown app:app /data
COPY --from=backend-build /out/api /usr/local/bin/api
WORKDIR /data
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]

FROM node:24-alpine AS frontend-build
WORKDIR /app
COPY backup-monitor-frontend/package.json backup-monitor-frontend/package-lock.json ./
RUN npm ci
COPY backup-monitor-frontend/src/ ./src/
COPY backup-monitor-frontend/public/ ./public/
COPY backup-monitor-frontend/index.html backup-monitor-frontend/tsconfig*.json backup-monitor-frontend/vite.config.ts ./
ENV VITE_API_BASE=""
RUN npm run build

FROM nginx:stable-alpine AS frontend
COPY backup-monitor-frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=frontend-build /app/dist/ /usr/share/nginx/html/
EXPOSE 80
