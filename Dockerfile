# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/locksmith .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata su-exec \
    && adduser -D -H -u 10001 -s /sbin/nologin locksmith \
    && mkdir -p /data \
    && chown locksmith:locksmith /data

WORKDIR /app
COPY --from=build /out/locksmith /app/locksmith

ENV DATABASE_PATH=/data/locksmith.db
ENV PORT=8080

EXPOSE 8080

USER locksmith
ENTRYPOINT ["/app/locksmith"]
