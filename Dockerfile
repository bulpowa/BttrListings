# Stage 1: build
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

# Cache dependencies before copying source
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bttrl-api ./cmd/api

# Stage 2: minimal runtime
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary
COPY --from=builder /build/bttrl-api ./

# Copy migrations — main.go resolves them relative to WORKDIR at startup
COPY --from=builder /build/internal/db/migrations ./internal/db/migrations

EXPOSE 8080

CMD ["./bttrl-api"]
