# rproxy - Reverse Proxy with Podman Backends and Let's Encrypt

This application acts as a reverse proxy that automatically discovers backend services running in Podman containers (via SSH) based on labels and manages TLS certificates for them using Let's Encrypt (via Gandi DNS challenge).

## Features

*   Dynamic backend discovery using Podman container labels (`exposed-port`, `exposed-fqdn`).
*   Automatic TLS certificate issuance and renewal via Let's Encrypt.
*   Uses Gandi LiveDNS for ACME DNS-01 challenge.
*   Built as a minimal container image.

## Prerequisites

*   [Podman](https://podman.io/) (including a running Podman machine if on macOS/Windows)
*   `make`
*   An SSH key configured for accessing the Podman machine/host.
*   A Gandi account with an API key and a domain managed by Gandi LiveDNS.

## Configuration

1.  **Copy `.env.example` to `.env`** (or create `.env` manually).
2.  **Edit `.env`** and fill in the **required** values:
    *   `GANDI_API_KEY`: Your Gandi LiveDNS API key.
    *   `ACME_EMAIL`: The email address for Let's Encrypt registration.
    *   `GANDI_ZONE`: Your base domain name managed by Gandi (e.g., `example.com`).
3.  Optionally, uncomment and set `PODMAN_SSH_USER` if it's not `core`.
4.  Optionally, uncomment and set `LEGO_STAGING=true` to use the Let's Encrypt staging environment for testing (recommended initially).

**Note:** SSH connection details (host, port, key path) are automatically detected by the `Makefile` using `podman machine inspect`. Certificates are stored in a named Podman volume (`rproxy-certs`).

## Usage (Makefile)

The `Makefile` provides convenient targets:

*   `make build`: Builds the container image (`rproxy:latest` by default).
*   `make run`: Runs the container interactively in the foreground. Useful for testing. Press `Ctrl+C` to stop. Uses the named volume for certificates.
*   `make deploy`: Runs the container detached in the background with `restart unless-stopped`. Uses the named volume for certificates. This is intended for deployment.
*   `make stop`: Stops the container started by `make deploy`.
*   `make rm`: Removes the stopped container.
*   `make clean`: Stops and removes the container.
*   `make logs`: Tails the logs of the detached container.
*   `make clean-certs-volume`: **DANGER!** Removes the named volume containing ACME keys and certificates. Use with extreme caution.
*   `make help`: Displays help and configured settings.

## Backend Container Labels

For `rproxy` to discover a backend container, the container must:

1.  Be running.
2.  Have the label `exposed-fqdn` set to the desired fully qualified domain name (e.g., `app.example.com`).
3.  Have the label `exposed-port` set to the internal port the application listens on (e.g., `8080`).

Example Podman run command for a backend:

```bash
podman run -d --name my-app \
  --label exposed-fqdn=app.example.com \
  --label exposed-port=8080 \
  my-backend-image
``` 