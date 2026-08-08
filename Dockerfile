# Multi-stage build: no private dependencies here, so no private-registry
# credentials or GOPRIVATE setup needed in the builder stage.
FROM golang:1.26-alpine AS builder
ENV CGO_ENABLED=0
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/app ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
COPY --from=builder /out/app /app
COPY --from=builder /build/migrations /migrations
ENV MIGRATIONS_DIR=/migrations
ENTRYPOINT ["/app"]
