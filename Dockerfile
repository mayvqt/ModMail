FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/modmail ./cmd/bot

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H appuser
COPY --from=build /out/modmail /app/modmail
VOLUME ["/data"]
ENV DB_PATH=/data/modmail.sqlite
USER appuser
ENTRYPOINT ["/app/modmail"]
