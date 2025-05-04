package certs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge/dns01"
)

const gandiAPI = "https://api.gandi.net"

// ManualGandiHTTPProvider implements challenge.Provider for Gandi LiveDNS via HTTP.
type ManualGandiHTTPProvider struct {
	apiKey  string
	baseZone string
	httpClient *http.Client
}

// NewManualGandiHTTPProvider creates a new provider instance.
func NewManualGandiHTTPProvider(apiKey, baseZone string) *ManualGandiHTTPProvider {
	trimmedZone := strings.TrimSpace(baseZone)
	trimmedZone = strings.TrimSuffix(trimmedZone, ".")
	return &ManualGandiHTTPProvider{
		apiKey:  apiKey,
		baseZone: trimmedZone,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Present creates the TXT record using Gandi LiveDNS API.
func (p *ManualGandiHTTPProvider) Present(domain, token, keyAuth string) error {
	fqdn, value := dns01.GetRecord(domain, keyAuth)
	recordName := strings.TrimSuffix(fqdn, "."+p.baseZone+".")
	if recordName == fqdn {
		return fmt.Errorf("could not derive record name from fqdn '%s' and base zone '%s'", fqdn, p.baseZone)
	}

	apiURL := fmt.Sprintf("%s/v5/livedns/domains/%s/records/%s", gandiAPI, p.baseZone, recordName)

	payload := map[string]interface{}{
		"rrset_type":   "TXT",
		"rrset_values": []string{value},
		"rrset_ttl":    300,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal gandi TXT record payload: %w", err)
	}

	slog.Info("Gandi Provider: Creating TXT record", "record", recordName, "zone", p.baseZone)

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create gandi API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send gandi API request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		slog.Error("Gandi API error creating TXT record", "status", resp.StatusCode, "body", string(bodyBytes), "record", recordName, "zone", p.baseZone)
		return fmt.Errorf("gandi API error creating TXT record: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	slog.Info("Gandi Provider: Successfully created TXT record", "record", recordName, "zone", p.baseZone)
	return nil
}

// CleanUp removes the TXT record using Gandi LiveDNS API.
func (p *ManualGandiHTTPProvider) CleanUp(domain, token, keyAuth string) error {
	fqdn, _ := dns01.GetRecord(domain, keyAuth)
	recordName := strings.TrimSuffix(fqdn, "."+p.baseZone+".")
	if recordName == fqdn {
		return fmt.Errorf("cleanup: could not derive record name from fqdn '%s' and base zone '%s'", fqdn, p.baseZone)
	}
	apiURL := fmt.Sprintf("%s/v5/livedns/domains/%s/records/%s/TXT", gandiAPI, p.baseZone, recordName)

	slog.Info("Gandi Provider: Deleting TXT record", "record", recordName, "zone", p.baseZone)

	req, err := http.NewRequest("DELETE", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create gandi API delete request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send gandi API delete request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		slog.Error("Gandi API error deleting TXT record", "status", resp.StatusCode, "body", string(bodyBytes), "record", recordName, "zone", p.baseZone)
		return fmt.Errorf("gandi API error deleting TXT record: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	slog.Info("Gandi Provider: Successfully deleted TXT record (or it was already gone)", "record", recordName, "zone", p.baseZone)
	return nil
}

// Timeout returns a reasonable timeout duration.
func (p *ManualGandiHTTPProvider) Timeout() (timeout, interval time.Duration) {
	return 10 * time.Minute, 30 * time.Second
} 