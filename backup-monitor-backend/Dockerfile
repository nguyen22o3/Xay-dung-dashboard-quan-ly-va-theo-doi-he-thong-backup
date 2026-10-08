FROM golang:1.27.1-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/api/ ./cmd/api/
COPY internal/ ./internal/
COPY ops/templates.go ops/backup-panel.sh ./ops/
COPY ops/templates/ ./ops/templates/

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
    -o /out/api ./cmd/api

FROM alpine:3.24
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