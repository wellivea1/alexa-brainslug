# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies are copied and downloaded before the source, so an edit to the
# source does not invalidate the module cache layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a static binary, which is what allows the runtime
# stage to hold nothing but the binary and its certificates. It works because
# the SQLite driver is pure Go.
# -trimpath keeps build machine paths out of the binary; -s -w drop the symbol
# table and DWARF data.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/brainslug ./cmd/brainslug

FROM alpine:3.24

# wget is used by the healthcheck below and ships in the base image.
RUN apk add --no-cache ca-certificates \
    && addgroup -S brainslug \
    && adduser -S -G brainslug brainslug \
    && mkdir -p /data \
    && chown -R brainslug:brainslug /data

COPY --from=build /out/brainslug /usr/local/bin/brainslug

# The working directory is owned by the runtime user so that a relative
# SQLITE_PATH degrades to a writable location instead of a permission error
# at startup.
WORKDIR /app
RUN chown brainslug:brainslug /app

USER brainslug

# Both processes bind all container interfaces so Docker can publish them. The
# Compose file binds each published port to host loopback; only the ASK port is
# intended for a path-scoped reverse proxy.
ENV SERVER_ADDR=:8080
ENV ADMIN_SERVER_ADDR=:8081

# The mounted volume. Overriding this from a .env file is the documented way
# to break a container deployment: see .env.example.
ENV SQLITE_PATH=/data/brainslug.db

EXPOSE 8080 8081

# Health is intentionally served by the private administrative listener.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --quiet --spider --tries=1 http://127.0.0.1:8081/health || exit 1

ENTRYPOINT ["brainslug"]
