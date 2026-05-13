# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/modmail ./cmd/bot

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -D -H -G app appuser \
    && mkdir -p /data \
    && chown -R appuser:app /app /data
COPY --from=build --chown=appuser:app /out/modmail /app/modmail
VOLUME ["/data"]
ENV DB_PATH=/data/modmail.sqlite
USER appuser:app
ENTRYPOINT ["/app/modmail"]
