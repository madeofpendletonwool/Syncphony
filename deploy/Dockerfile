# syntax=docker/dockerfile:1
# Build from the repo root:  docker build -f deploy/Dockerfile .

FROM node:24-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
COPY api/ /src/api/
RUN pnpm build

FROM golang:1.27-alpine AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/dist/ ./internal/webui/dist/
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/syncphony ./cmd/syncphony
# An empty /data owned by nonroot, so a fresh volume mounted there is writable.
RUN mkdir -p /out/data

# Alpine rather than distroless, for ffmpeg: players that can't decode a
# song's format (iOS Safari and Spotify's Ogg Vorbis) get it transcoded.
# nonroot is UID 65532, as in distroless, so existing /data volumes stay
# writable.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates ffmpeg tzdata \
    && addgroup -S -g 65532 nonroot \
    && adduser -S -D -H -u 65532 -G nonroot -h /data nonroot
COPY --from=server /out/syncphony /usr/local/bin/syncphony
COPY --from=server --chown=nonroot:nonroot /out/data /data
ENV SYNCPHONY_ADDR=:8080 \
    SYNCPHONY_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/syncphony"]
