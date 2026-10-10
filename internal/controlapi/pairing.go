package controlapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"path/filepath"
	"regexp"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/control"
	"github.com/wellivea1/alexa-brainslug/internal/securefile"
)

var identityName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// CreatePairing creates an offline private CA and distinct gateway/worker
// identities in a dedicated directory. Existing files are never replaced.
func CreatePairing(directory, serverName string, workers []string) ([]Identity, error) {
	if directory == "" || serverName == "" || len(workers) == 0 {
		return nil, control.ErrInvalid
	}
	seen := map[string]bool{RoleGateway: true, roleController: true, "ca": true}
	for _, name := range workers {
		if !identityName.MatchString(name) || seen[name] {
			return nil, control.ErrInvalid
		}
		seen[name] = true
	}
	if err := securefile.PrepareDirectory(directory); err != nil {
		return nil, err
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Brainslug private task CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(2, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	if err := writeIdentity(directory, "ca", caDER, caKey); err != nil {
		return nil, err
	}
	var identities []Identity
	names := append([]string{roleController, RoleGateway}, workers...)
	for i, name := range names {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(0, 6, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		if name == roleController {
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			if ip := net.ParseIP(serverName); ip != nil {
				template.IPAddresses = []net.IP{ip}
			} else {
				template.DNSNames = []string{serverName}
			}
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caTemplate, &key.PublicKey, caKey)
		if err != nil {
			return nil, err
		}
		if err := writeIdentity(directory, name, der, key); err != nil {
			return nil, err
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		identity := Identity{Name: name, Fingerprint: Fingerprint(cert), Enabled: true, Role: RoleWorker, HostID: name}
		if name == RoleGateway {
			identity.Role = RoleGateway
			identity.HostID = ""
			identity.Owner = true
			identity.Principals = []string{"owner:local"}
		}
		if name != roleController {
			identities = append(identities, identity)
		}
	}
	data, err := json.MarshalIndent(identities, "", "  ")
	if err != nil {
		return nil, err
	}
	return identities, securefile.CreateExclusive(filepath.Join(directory, "identities.json"), append(data, '\n'))
}

func writeIdentity(directory, name string, der []byte, key *ecdsa.PrivateKey) error {
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := securefile.CreateExclusive(filepath.Join(directory, name+".key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})); err != nil {
		return err
	}
	return securefile.CreateExclusive(filepath.Join(directory, name+".pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
