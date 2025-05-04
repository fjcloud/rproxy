# Stage 1: Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy the entire source code first
COPY . .

# Initialize the module and download dependencies inside the container
# Replace 'rproxy' if your module name is different
RUN go mod init rproxy && go mod tidy

# Build the application, targeting the main package
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

# USER nobody # Optional: Run as non-root user for security

# Environment variables expected by the application at runtime (examples)
# These should typically be set during container run/deployment, not here.
# ENV PODMAN_SSH_USER=core
# ENV PODMAN_SSH_HOST=your_podman_host
# ENV PODMAN_SSH_PORT=22
# ENV PODMAN_SSH_KEY=/ssh/id_rsa # Mount the key to /ssh/id_rsa
# ENV GANDI_API_KEY=your_gandi_key
# ENV ACME_EMAIL=your_email@example.com
# ENV GANDI_ZONE=your_zone.com
# ENV CERTS_DIR=/certs
# ENV LEGO_STAGING=false # Or true for testing 