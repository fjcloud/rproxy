package sshclient

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client manages SSH connections and commands.
type Client struct {
	config *ssh.ClientConfig
	addr   string
}

const sshKeyPathInsideContainer = "/ssh/id_rsa" // Define constant for the path

// New creates a new SSH client.
// func New(user, host, port, identityFile string) (*Client, error) { // Removed identityFile parameter
func New(user, host, port string) (*Client, error) {
	authMethod, err := getPrivateKeyAuthMethod(sshKeyPathInsideContainer) // Use the constant path
	if err != nil {
		return nil, fmt.Errorf("failed to prepare SSH auth method: %w", err)
	}

	sshConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			authMethod,
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: Consider stricter host key checking
		Timeout:         10 * time.Second, // Connection timeout
	}

	addr := net.JoinHostPort(host, port)

	slog.Info("SSH Client configured", "user", user, "address", addr, "keyPath", sshKeyPathInsideContainer)
	return &Client{
		config: sshConfig,
		addr:   addr,
	}, nil
}

// RunCommand executes a command over SSH and returns its output.
func (c *Client) RunCommand(command string) ([]byte, error) {
	client, err := ssh.Dial("tcp", c.addr, c.config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SSH server %s: %w", c.addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	output, err := session.Output(command)
	if err != nil {
		outputStr := ""
		if len(output) > 0 {
			outputStr = fmt.Sprintf(". Output/Stderr: %s", string(output))
		}
		return nil, fmt.Errorf("failed to run command via SSH '%s': %w%s", command, err, outputStr)
	}
	return output, nil
}

// getPrivateKeyAuthMethod loads an SSH key.
func getPrivateKeyAuthMethod(keyPath string) (ssh.AuthMethod, error) {
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		// Add more context to the error message
		return nil, fmt.Errorf("failed to read private key file %q (ensure it's mounted correctly): %w", keyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		// TODO: Add support for passphrase-protected keys if needed
		return nil, fmt.Errorf("failed to parse private key %s: %w", keyPath, err)
	}
	return ssh.PublicKeys(signer), nil
} 