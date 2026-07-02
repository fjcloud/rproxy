# Stage 1: Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy dependency files first for better layer caching
COPY go.mod go.sum ./

# Download dependencies (cached unless go.mod/go.sum change)
RUN go mod download

# Copy the entire source code
COPY . .

# Build the application
# -ldflags="-w -s" removes debug information and symbols for a smaller binary
# CGO_ENABLED=0 ensures static linking (useful for scratch/distroless)
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /rproxy cmd/rproxy/main.go

# Stage 2: Final stage
# Use a minimal base image like ubi9-micro from Docker Hub
FROM docker.io/redhat/ubi9-micro AS final

# Copy CA certificates from the builder stage for TLS verification
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary from the build stage
COPY --from=builder /rproxy /rproxy

# Expose the HTTPS port
EXPOSE 443

# Set the entrypoint
ENTRYPOINT ["/rproxy"]

# Environment variables expected by the application at runtime (examples)
# These should typically be set during container run/deployment, not here.
# ENV PODMAN_SSH_USER=core
# ENV PODMAN_SSH_HOST=your_podman_host
# ENV PODMAN_SSH_PORT=22
# ENV PODMAN_SSH_KEY=/ssh/id_rsa # Mount the key to /ssh/id_rsa
# ENV GANDI_PAT=your_gandi_pat           # Gandi Personal Access Token (uses Bearer auth)
# ENV ACME_EMAIL=your_email@example.com
# ENV GANDI_ZONE=your_zone.com
# ENV CERTS_DIR=/certs
# ENV LEGO_STAGING=false # Or true for testing
