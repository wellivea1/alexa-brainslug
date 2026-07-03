FROM golang:1.26-alpine AS build

WORKDIR /src
RUN apk add --no-cache ca-certificates

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/brainslug ./cmd/brainslug

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S brainslug \
    && adduser -S -G brainslug brainslug \
    && mkdir -p /data \
    && chown -R brainslug:brainslug /data

WORKDIR /app
COPY --from=build /out/brainslug /usr/local/bin/brainslug

USER brainslug
ENV SERVER_ADDR=:8080
ENV SQLITE_PATH=/data/brainslug.db

EXPOSE 8080
ENTRYPOINT ["brainslug"]
