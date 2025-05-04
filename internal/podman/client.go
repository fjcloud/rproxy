package podman

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"rproxy/internal/sshclient" // Assuming module path is rproxy
	"strings"
)

// --- Structs for Podman Data --- 

// Structs match the relevant fields from podman list/inspect
type InspectNetworkSettings struct {
	Networks map[string]struct {
		IPAddress string `json:"IPAddress"`
	}
}
type InspectOutput struct {
	Id              string                 `json:"Id"`
	NetworkSettings InspectNetworkSettings `json:"NetworkSettings"`
}

// ContainerInfo holds data retrieved about a container.
type ContainerInfo struct {
	ID          string
	Name        string
	ExposedPort string
	FQDN        string
}

// --- Podman Client --- 

// Client interacts with Podman via SSH.
type Client struct {
	ssh *sshclient.Client
}

// New creates a new Podman client.
func New(sshClient *sshclient.Client) *Client {
	return &Client{ssh: sshClient}
}

// ListContainers lists running containers with required labels.
func (c *Client) ListContainers() ([]ContainerInfo, error) {
	// Use tab separator for potentially complex FQDNs/Names
	cmd := `podman container list --filter label=exposed-port --filter label=exposed-fqdn --filter status=running --no-trunc --format '{{.ID}}\t{{.Names}}\t{{index .Labels "exposed-port"}}\t{{index .Labels "exposed-fqdn"}}'`

	output, err := c.ssh.RunCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers via ssh: %w", err)
	}

	var containers []ContainerInfo
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "\t", 4) // Split by tab
		if len(parts) == 4 {
			name := strings.TrimPrefix(parts[1], "/")
			fqdn := strings.TrimSpace(parts[3])
			if name != "" && parts[0] != "" && parts[2] != "" && fqdn != "" {
				containers = append(containers, ContainerInfo{
					ID:          parts[0],
					Name:        name,
					ExposedPort: parts[2],
					FQDN:        fqdn,
				})
			} else {
				slog.Warn("Missing required info in list output line", "line", line)
			}
		} else {
			slog.Warn("Could not parse list output line (expected 4 tab-separated parts)", "line", line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning podman list output: %w", err)
	}

	return containers, nil
}

// InspectContainer gets details for a specific container ID.
func (c *Client) InspectContainer(containerID string) (*InspectOutput, error) {
	cmd := fmt.Sprintf("podman container inspect %s --format json", containerID)
	output, err := c.ssh.RunCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %s via ssh: %w", containerID, err)
	}

	var inspectDataSlice []InspectOutput // Inspect returns an array
	if err := json.Unmarshal(output, &inspectDataSlice); err != nil {
		slog.Error("Error parsing inspect JSON array", "containerID", containerID, "error", err, "output", string(output))
		return nil, fmt.Errorf("failed to parse inspect json for %s: %w", containerID, err)
	}

	if len(inspectDataSlice) != 1 {
		slog.Warn("Expected 1 container in inspect output", "containerID", containerID, "count", len(inspectDataSlice))
		return nil, fmt.Errorf("unexpected number of results (%d) inspecting container %s", len(inspectDataSlice), containerID)
	}

	return &inspectDataSlice[0], nil
} 