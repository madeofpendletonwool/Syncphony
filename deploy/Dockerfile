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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/syncphony /usr/local/bin/syncphony
ENV SYNCPHONY_ADDR=:8080 \
    SYNCPHONY_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/syncphony"]
