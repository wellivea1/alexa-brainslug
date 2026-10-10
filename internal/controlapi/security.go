// Package controlapi implements the authenticated, versioned gateway and worker
// transports. Controller use cases remain independent of HTTP and WebSockets.
package controlapi

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strings"
)

// Service roles enroll independent gateway and execution credentials.
const (
	RoleGateway    = "gateway"
	RoleWorker     = "worker"
	roleController = "controller"
	fieldPrincipal = "principal"
)

// TLSFiles pins the owner's trust roots and one enrolled process identity.
type TLSFiles struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
	CA          string `json:"ca"`
	ServerName  string `json:"server_name"`
}

// Identity is an explicitly enrolled, revocable leaf-certificate fingerprint.
type Identity struct {
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	Role        string   `json:"role"`
	HostID      string   `json:"host_id"`
	Principals  []string `json:"principals"`
	Enabled     bool     `json:"enabled"`
	Owner       bool     `json:"owner"`
}

// Fingerprint identifies a certificate without exposing its key or subject.
func Fingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

// LoadTLS requires mutual authentication and a configured CA; verification cannot be disabled.
func LoadTLS(files TLSFiles, server bool) (*tls.Config, error) {
	if files.Certificate == "" || files.Key == "" || files.CA == "" {
		return nil, errors.New("certificate, key, and pinned CA files are required")
	}
	certificate, err := tls.LoadX509KeyPair(files.Certificate, files.Key)
	if err != nil {
		return nil, errors.New("could not load the enrolled TLS identity")
	}
	// #nosec G304 -- the CA file is explicit private operator configuration.
	data, err := os.ReadFile(files.CA)
	if err != nil {
		return nil, errors.New("could not read the pinned CA file")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("pinned CA file contains no certificate")
	}
	configuration := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, RootCAs: pool, ServerName: files.ServerName}
	if server {
		configuration.ClientCAs = pool
		configuration.ClientAuth = tls.RequireAndVerifyClientCert
		configuration.ServerName = ""
	}
	return configuration, nil
}

func authenticated(r *http.Request, identities []Identity) (Identity, bool) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return Identity{}, false
	}
	fingerprint := Fingerprint(r.TLS.PeerCertificates[0])
	for _, identity := range identities {
		if identity.Enabled && strings.EqualFold(identity.Fingerprint, fingerprint) {
			return identity, true
		}
	}
	return Identity{}, false
}

func permitsPrincipal(identity Identity, key string) bool {
	for _, allowed := range identity.Principals {
		if allowed == key {
			return true
		}
	}
	return false
}
