# syntax=docker/dockerfile:1

# drenyra-engram — standalone Go memory engine (ADT-001, v0.2 foundation).
#
# Multi-stage build: Go builder -> minimal Alpine runtime.
# Fiscal convention: no monetary fields exist in this engine; observation
# content is structured text. Nothing here touches money.
#
# The engine binds 127.0.0.1:8787 by default (fail closed — local agents only).
# Inside the container we must override to 0.0.0.0:8733 so the compose network
# and healthcheck can reach it.

FROM golang:1.26-alpine AS builder
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/drenyra-engram ./cmd/drenyra-engram

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=builder /out/drenyra-engram /usr/local/bin/drenyra-engram
RUN mkdir -p /data
ENV PORT=8733
ENV DRENYRA_ENGRAM_DB=/data/engram.db
EXPOSE 8733
VOLUME /data
ENTRYPOINT ["drenyra-engram"]
CMD ["serve", "--addr", "0.0.0.0:8733", "--db", "/data/engram.db"]
