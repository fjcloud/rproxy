package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"rproxy/internal/config" // Assuming module path is rproxy
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

// --- ACME User --- 

type ACMEUser struct {
	Email        string
	Registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *ACMEUser) GetEmail() string {
	return u.Email
}
func (u *ACMEUser) GetRegistration() *registration.Resource {
	return u.Registration
}
func (u *ACMEUser) GetPrivateKey() crypto.PrivateKey {
	return u.key
}

// --- CertManager --- 

const certificatesPath = "/certs"         // Hardcoded path for certs volume
const acmeAccountKeyFile = "acme_account.key" // Filename for the ACME account key

type Manager struct {
	certs       map[string]*tls.Certificate // In-memory cache: fqdn -> cert
	mu          sync.RWMutex
	legoUser    *ACMEUser
	legoClient  *lego.Client
	renewBefore time.Duration
}

// loadOrCreateACMEKey tries to load the key, generates and saves if not found.
func loadOrCreateACMEKey() (crypto.PrivateKey, error) {
	keyPath := filepath.Join(certificatesPath, acmeAccountKeyFile)
	pemData, err := os.ReadFile(keyPath)
	if err == nil {
		// Key file exists, try to parse it
		block, _ := pem.Decode(pemData)
		if block == nil || block.Type != "EC PRIVATE KEY" {
			return nil, fmt.Errorf("failed to decode PEM block containing ACME account key from %s", keyPath)
		}
		privateKey, parseErr := x509.ParseECPrivateKey(block.Bytes)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse EC private key from %s: %w", keyPath, parseErr)
		}
		slog.Info("Loaded existing ACME account private key", "path", keyPath)
		return privateKey, nil
	} else if os.IsNotExist(err) {
		// Key file doesn't exist, generate a new one
		slog.Info("ACME account private key not found, generating a new one...", "path", keyPath)
		privateKey, genErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if genErr != nil {
			return nil, fmt.Errorf("failed to generate new ACME private key: %w", genErr)
		}

		// Save the new key
		keyBytes, marshalErr := x509.MarshalECPrivateKey(privateKey)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal new ACME private key: %w", marshalErr)
		}
		pemBlock := &pem.Block{
			Type:  "EC PRIVATE KEY",
			Bytes: keyBytes,
		}
		if writeErr := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0600); writeErr != nil {
			slog.Error("Failed to save newly generated ACME account private key", "path", keyPath, "error", writeErr)
			// Return the generated key anyway, but log the error
			return privateKey, nil 
		}
		slog.Info("Successfully generated and saved new ACME account private key", "path", keyPath)
		return privateKey, nil
	} else {
		// Other error reading file
		return nil, fmt.Errorf("error reading ACME account key file %s: %w", keyPath, err)
	}
}

// NewManager initializes the certificate manager.
func NewManager(cfg *config.Config) (*Manager, error) {
	// Ensure certificates directory exists first
	if err := os.MkdirAll(certificatesPath, 0700); err != nil {
		slog.Warn("Could not create certs directory", "path", certificatesPath, "error", err)
		// Allow continuation, maybe permissions are fixed later or volume is read-only
	}

	// Load or create the ACME private key
	privateKey, err := loadOrCreateACMEKey()
	if err != nil {
		return nil, fmt.Errorf("failed to load or create ACME private key: %w", err)
	}

	// Create ACME user WITH PERSISTENT KEY
	// privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader) // REMOVED: Don't generate every time
	acmeUser := &ACMEUser{
		Email: cfg.ACMEEmail,
		key:   privateKey, // Use the loaded or newly generated key
	}

	// Create Lego Config
	legoCfg := lego.NewConfig(acmeUser)
	if cfg.ACMEStaging {
		legoCfg.CADirURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
		slog.Info("Using Let's Encrypt staging environment.")
	} else {
		legoCfg.CADirURL = "https://acme-v02.api.letsencrypt.org/directory"
		slog.Info("Using Let's Encrypt production environment.")
	}
	legoCfg.Certificate.KeyType = certcrypto.EC256

	// Create Lego Client
	client, err := lego.NewClient(legoCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create ACME client: %w", err)
	}

	// Use Manual Gandi HTTP Provider
	slog.Info("Setting up Manual Gandi HTTP provider", "zone", cfg.GandiZone)
	manualProvider := NewManualGandiHTTPProvider(cfg.GandiAPIKey, cfg.GandiZone)
	resolverOpt := dns01.AddRecursiveNameservers([]string{"1.1.1.1:53", "8.8.8.8:53"})
	err = client.Challenge.SetDNS01Provider(manualProvider, resolverOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to set Manual Gandi DNS01 provider with resolvers: %w", err)
	}

	// Register or Resolve ACME User
	// Try resolving first, as the key should now be persistent
	slog.Info("Resolving ACME account...")
	acmeUser.Registration, err = client.Registration.ResolveAccountByKey()
	if err != nil {
		slog.Warn("Failed to resolve ACME account by key, attempting registration...", "error", err)
		// log.Println("[INFO] Registering ACME account...") // Keep this log internal to lego
		acmeUser.Registration, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			// If both resolve and register fail, it's a real error
			return nil, fmt.Errorf("failed to resolve or register ACME account: %w", err)
		}
		slog.Info("ACME account registered successfully.")
	} else {
		slog.Info("Resolved existing ACME account successfully.")
	}

	manager := &Manager{
		certs:       make(map[string]*tls.Certificate),
		legoUser:    acmeUser,
		legoClient:  client,
		renewBefore: cfg.RenewBefore,
	}

	slog.Info("Certificate manager initialized.")
	return manager, nil
}

// loadCertFromFile loads cert from file, returns expiry time and caches it.
func (m *Manager) loadCertFromFile(fqdn string) (time.Time, error) {
	certFile := filepath.Join(certificatesPath, fqdn+".crt")
	keyFile := filepath.Join(certificatesPath, fqdn+".key")

	certData, err := os.ReadFile(certFile)
	if err != nil {
		return time.Time{}, err
	}
	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		return time.Time{}, err
	}

	tlsCert, err := tls.X509KeyPair(certData, keyData)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to load key pair from file for %s: %w", fqdn, err)
	}

	if len(tlsCert.Certificate) == 0 {
		return time.Time{}, fmt.Errorf("no certificate found in %s", certFile)
	}
	x509Cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse certificate from %s: %w", certFile, err)
	}

	m.mu.Lock()
	m.certs[fqdn] = &tlsCert
	m.mu.Unlock()

	return x509Cert.NotAfter, nil
}

// obtainOrRenewCert obtains or renews cert using Lego.
func (m *Manager) obtainOrRenewCert(fqdn string) error {
	slog.Info("ACME: Attempting to obtain/renew certificate", "fqdn", fqdn)

	if m.legoClient == nil {
		return fmt.Errorf("Lego client not initialized in CertManager")
	}

	slog.Info("ACME: Requesting certificate", "domains", []string{fqdn})
	request := certificate.ObtainRequest{
		Domains: []string{fqdn},
		Bundle:  true,
	}
	certRes, err := m.legoClient.Certificate.Obtain(request)
	if err != nil {
		slog.Error("ACME: Failed to obtain certificate", "fqdn", fqdn, "error", err)
		return fmt.Errorf("failed to obtain certificate for %s: %w", fqdn, err)
	}

	certFile := filepath.Join(certificatesPath, fqdn+".crt")
	keyFile := filepath.Join(certificatesPath, fqdn+".key")

	err = os.WriteFile(certFile, certRes.Certificate, 0600)
	if err != nil {
		return fmt.Errorf("failed to save certificate to %s: %w", certFile, err)
	}
	err = os.WriteFile(keyFile, certRes.PrivateKey, 0600)
	if err != nil {
		return fmt.Errorf("failed to save private key to %s: %w", keyFile, err)
	}

	slog.Info("Successfully obtained and saved certificate", "fqdn", fqdn)

	_, err = m.loadCertFromFile(fqdn) // Load and cache
	if err != nil {
		slog.Error("Error loading newly obtained certificate into cache", "fqdn", fqdn, "error", err)
	}

	return nil
}

// CheckAndManageCert checks cert file, triggers obtain/renew if needed.
func (m *Manager) CheckAndManageCert(fqdn string) {
	needsObtain := false
	certFile := filepath.Join(certificatesPath, fqdn+".crt")

	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		slog.Info("CertMaintenance: Certificate file not found, triggering initial obtainment", "fqdn", fqdn)
		needsObtain = true
	} else if err != nil {
		slog.Error("CertMaintenance: Error checking certificate file", "fqdn", fqdn, "error", err)
		return
	} else {
		expiry, err := m.loadCertFromFile(fqdn)
		if err != nil {
			slog.Error("CertMaintenance: Error loading existing certificate file", "fqdn", fqdn, "error", err)
		} else {
			if time.Until(expiry) < m.renewBefore {
				slog.Info("CertMaintenance: Certificate nearing expiry, triggering renewal", "fqdn", fqdn, "expiry", expiry, "renew_before", m.renewBefore)
				needsObtain = true
			}
		}
	}

	if needsObtain {
		err := m.obtainOrRenewCert(fqdn)
		if err != nil {
			slog.Error("CertMaintenance: Error during certificate obtain/renew", "fqdn", fqdn, "error", err)
		}
	}
}

// GetCertificateForSNI retrieves a certificate from cache or loads from file.
func (m *Manager) GetCertificateForSNI(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if hello.ServerName == "" {
		slog.Warn("TLS ClientHello missing ServerName (SNI)")
		return nil, fmt.Errorf("missing server name (SNI)")
	}

	fqdn := hello.ServerName
	m.mu.RLock()
	cert, exists := m.certs[fqdn]
	m.mu.RUnlock()

	if !exists {
		slog.Info("TLS: Certificate not in cache, attempting load from file", "sni", fqdn)
		_, err := m.loadCertFromFile(fqdn)
		if err == nil {
			m.mu.RLock()
			cert, exists = m.certs[fqdn]
			m.mu.RUnlock()
			if !exists {
				slog.Error("TLS: Certificate inconsistent after loading", "sni", fqdn)
				return nil, fmt.Errorf("certificate for %s inconsistent after loading", fqdn)
			}
		} else {
			if os.IsNotExist(err) {
				slog.Info("TLS: Certificate not found in cache or on disk", "sni", fqdn)
			} else {
				slog.Error("TLS: Failed to load certificate from file", "sni", fqdn, "error", err)
			}
			return nil, fmt.Errorf("certificate for %s not available", fqdn)
		}
	}

	return cert, nil
} 