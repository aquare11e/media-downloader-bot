# Build stage
FROM golang:1.23-alpine3.21 AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod file
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o coordinator-service ./coordinator-service/main.go

# Final stage
FROM alpine:3.21

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/coordinator-service/main coordinator-service

# Create a non-root user
RUN adduser -D -g '' appuser
USER appuser

# Set non-sensitive environment variables
ENV SERVICE_PORT="50051"
ENV TRANSMISSION_URL=""
ENV PLEX_URL=""
ENV REDIS_URL=""
ENV FILMS_DIR_PATH=""
ENV SERIES_DIR_PATH=""
ENV CARTOONS_DIR_PATH=""
ENV CARTOONS_SERIES_DIR_PATH=""
ENV SHORTS_DIR_PATH=""
ENV SWITCH_DIR_PATH=""

# Run the application
CMD ["./coordinator-service"] 