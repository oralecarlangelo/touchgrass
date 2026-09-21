# Multi-stage build: web UI (node) + Go binary -> slim runtime.
FROM node:24-alpine AS web

WORKDIR /web

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /web/dist ./web/dist

ARG VERSION=docker
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o /touchgrass ./cmd/touchgrass

FROM alpine:3

RUN adduser -D -H touchgrass && mkdir -p /data && chown touchgrass:touchgrass /data
USER touchgrass

COPY --from=builder /touchgrass /usr/local/bin/touchgrass

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s CMD wget -q -O /dev/null http://127.0.0.1:8080/api/health || exit 1

ENTRYPOINT ["touchgrass", "serve"]
