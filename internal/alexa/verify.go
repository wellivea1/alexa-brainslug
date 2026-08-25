package alexa

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	signatureCertChainURLHeader = "SignatureCertChainUrl"
	signature256Header          = "Signature-256"
	alexaCertificateHost        = "s3.amazonaws.com"
	alexaCertificatePathPrefix  = "/echo.api/"
	alexaSigningDNSName         = "echo-api.amazon.com"
	requestTimestampTolerance   = 150 * time.Second
	certificateFetchTimeout     = 4 * time.Second
	certificateResponseLimit    = 64 << 10
	certificateCacheLimit       = 16
)

var (
	// ErrInvalidCertificateURL means the request supplied a certificate URL
	// outside Amazon's documented HTTPS S3 location.
	ErrInvalidCertificateURL = errors.New("invalid Alexa certificate URL")

	// ErrCertificateFetch means the signing certificate could not be loaded.
	ErrCertificateFetch = errors.New("could not fetch Alexa certificate")

	// ErrInvalidCertificate means the signing chain, validity, or SAN failed.
	ErrInvalidCertificate = errors.New("invalid Alexa signing certificate")

	// ErrInvalidSignature means Signature-256 did not authenticate the body.
	ErrInvalidSignature = errors.New("invalid Alexa request signature")

	// ErrInvalidTimestamp means the signed request timestamp was missing,
	// malformed, or outside Amazon's maximum 150-second tolerance.
	ErrInvalidTimestamp = errors.New("invalid Alexa request timestamp")
)

// VerificationConfig controls how incoming requests are authenticated.
type VerificationConfig struct {
	// VerifyRequests enables certificate, signature, and timestamp validation.
	VerifyRequests bool

	// AllowedSkillID, when set, restricts the endpoint to one skill. This check
	// is applied even when signature verification is disabled for a controlled
	// local test, but a skill ID alone is not authentication.
	AllowedSkillID string

	// HTTPClient fetches the Amazon certificate chain. Nil uses a hardened
	// client with a bounded timeout and validated redirects.
	HTTPClient *http.Client

	// RootCAs overrides the host trust store. It exists for deterministic tests
	// and private trust environments; nil uses the system roots.
	RootCAs *x509.CertPool

	// Now overrides the clock for deterministic tests. Nil uses time.Now.
	Now func() time.Time
}

// RequestVerifier validates Alexa requests and caches verified signing
// certificates for the lifetime of the handler.
type RequestVerifier struct {
	cfg VerificationConfig

	mu    sync.RWMutex
	cache map[string]cachedCertificate
}

type cachedCertificate struct {
	certificate *x509.Certificate
	validFrom   time.Time
	validUntil  time.Time
	cachedAt    time.Time
}

type verifiedCertificate struct {
	leaf       *x509.Certificate
	validFrom  time.Time
	validUntil time.Time
}

// NewRequestVerifier builds a verifier with an isolated bounded certificate
// cache.
func NewRequestVerifier(cfg VerificationConfig) *RequestVerifier {
	return &RequestVerifier{cfg: cfg, cache: make(map[string]cachedCertificate)}
}

// VerifyRequest authenticates one request without retaining a certificate
// cache. Handler instances use RequestVerifier directly so normal traffic can
// reuse an already validated signing certificate.
func VerifyRequest(r *http.Request, body []byte, envelope *RequestEnvelope, cfg VerificationConfig) error {
	return NewRequestVerifier(cfg).Verify(r, body, envelope)
}

// Verify authenticates the request body, timestamp, certificate chain, and
// expected skill application ID.
func (v *RequestVerifier) Verify(r *http.Request, body []byte, envelope *RequestEnvelope) error {
	if envelope == nil {
		return errors.New("alexa request envelope is nil")
	}

	if allowed := strings.TrimSpace(v.cfg.AllowedSkillID); allowed != "" {
		if got := envelope.ApplicationID(); got != allowed {
			return errors.New("alexa skill ID is not allowed")
		}
	}

	if !v.cfg.VerifyRequests {
		return nil
	}
	if r == nil {
		return errors.New("alexa HTTP request is nil")
	}

	certificateURL, err := normalizeCertificateURL(r.Header.Get(signatureCertChainURLHeader))
	if err != nil {
		return err
	}
	signature, err := decodeSignature(r.Header.Get(signature256Header))
	if err != nil {
		return err
	}

	now := v.now()
	certificate, err := v.signingCertificate(r.Context(), certificateURL, now)
	if err != nil {
		return err
	}
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("%w: signing key is not RSA", ErrInvalidCertificate)
	}

	digest := sha256.Sum256(body)
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return ErrInvalidSignature
	}
	if err := verifyTimestamp(envelope.Request.Timestamp, now); err != nil {
		return err
	}
	return nil
}

func (v *RequestVerifier) now() time.Time {
	if v.cfg.Now != nil {
		return v.cfg.Now()
	}
	return time.Now()
}

func (v *RequestVerifier) signingCertificate(ctx context.Context, certificateURL string, now time.Time) (*x509.Certificate, error) {
	if certificate := v.cached(certificateURL, now); certificate != nil {
		return certificate, nil
	}

	chainPEM, err := v.fetchCertificateChain(ctx, certificateURL)
	if err != nil {
		return nil, err
	}
	certificates, err := parseCertificateChain(chainPEM)
	if err != nil {
		return nil, err
	}
	verified, err := verifyCertificateChain(certificates, v.cfg.RootCAs, now)
	if err != nil {
		return nil, err
	}
	v.cacheCertificate(certificateURL, verified, now)
	return verified.leaf, nil
}

func (v *RequestVerifier) cached(certificateURL string, now time.Time) *x509.Certificate {
	v.mu.RLock()
	entry, ok := v.cache[certificateURL]
	v.mu.RUnlock()
	if !ok {
		return nil
	}
	if now.Before(entry.validFrom) || !now.Before(entry.validUntil) {
		v.mu.Lock()
		delete(v.cache, certificateURL)
		v.mu.Unlock()
		return nil
	}
	return entry.certificate
}

func (v *RequestVerifier) cacheCertificate(certificateURL string, certificate verifiedCertificate, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if len(v.cache) >= certificateCacheLimit {
		oldestURL := ""
		oldestTime := now
		for cachedURL, entry := range v.cache {
			if oldestURL == "" || entry.cachedAt.Before(oldestTime) {
				oldestURL = cachedURL
				oldestTime = entry.cachedAt
			}
		}
		delete(v.cache, oldestURL)
	}
	v.cache[certificateURL] = cachedCertificate{
		certificate: certificate.leaf,
		validFrom:   certificate.validFrom,
		validUntil:  certificate.validUntil,
		cachedAt:    now,
	}
}

func (v *RequestVerifier) fetchCertificateChain(ctx context.Context, certificateURL string) ([]byte, error) {
	client := v.cfg.HTTPClient
	if client == nil {
		client = newCertificateHTTPClient()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, certificateURL, http.NoBody) //nolint:gosec // certificateURL passed strict Amazon host and path validation.
	if err != nil {
		return nil, fmt.Errorf("%w: build request", ErrCertificateFetch)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request failed", ErrCertificateFetch)
	}
	if response.Body == nil {
		return nil, fmt.Errorf("%w: response body is missing", ErrCertificateFetch)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.Request != nil && response.Request.URL != nil {
		if _, err := normalizeCertificateURL(response.Request.URL.String()); err != nil {
			return nil, fmt.Errorf("%w: redirected outside the allowed certificate location", ErrCertificateFetch)
		}
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP status %d", ErrCertificateFetch, response.StatusCode)
	}
	limited := io.LimitReader(response.Body, certificateResponseLimit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%w: read response", ErrCertificateFetch)
	}
	if len(body) > certificateResponseLimit {
		return nil, fmt.Errorf("%w: response exceeds size limit", ErrCertificateFetch)
	}
	return body, nil
}

func newCertificateHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: certificateFetchTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           10,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    certificateFetchTimeout,
		ResponseHeaderTimeout:  certificateFetchTimeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 16 << 10,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   certificateFetchTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many certificate redirects")
			}
			_, err := normalizeCertificateURL(request.URL.String())
			return err
		},
	}
}

func normalizeCertificateURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", ErrInvalidCertificateURL
	}
	if parsed.Opaque != "" || parsed.User != nil {
		return "", ErrInvalidCertificateURL
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", ErrInvalidCertificateURL
	}
	if !strings.EqualFold(parsed.Hostname(), alexaCertificateHost) {
		return "", ErrInvalidCertificateURL
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", ErrInvalidCertificateURL
	}

	cleanPath := path.Clean(parsed.Path)
	if !strings.HasPrefix(cleanPath, alexaCertificatePathPrefix) {
		return "", ErrInvalidCertificateURL
	}

	parsed.Scheme = "https"
	parsed.Host = alexaCertificateHost
	parsed.Path = cleanPath
	parsed.RawPath = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func decodeSignature(rawSignature string) ([]byte, error) {
	rawSignature = strings.TrimSpace(rawSignature)
	if rawSignature == "" {
		return nil, ErrInvalidSignature
	}
	signature, err := base64.StdEncoding.DecodeString(rawSignature)
	if err != nil || len(signature) == 0 {
		return nil, ErrInvalidSignature
	}
	return signature, nil
}

func parseCertificateChain(chainPEM []byte) ([]*x509.Certificate, error) {
	var certificates []*x509.Certificate
	remaining := chainPEM
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			if len(bytes.TrimSpace(remaining)) != 0 {
				return nil, fmt.Errorf("%w: malformed PEM", ErrInvalidCertificate)
			}
			break
		}
		remaining = rest
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: unexpected PEM block", ErrInvalidCertificate)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: parse certificate", ErrInvalidCertificate)
		}
		certificates = append(certificates, certificate)
	}
	if len(certificates) == 0 {
		return nil, fmt.Errorf("%w: chain is empty", ErrInvalidCertificate)
	}
	return certificates, nil
}

func verifyCertificateChain(certificates []*x509.Certificate, roots *x509.CertPool, now time.Time) (verifiedCertificate, error) {
	leaf := certificates[0]
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return verifiedCertificate{}, fmt.Errorf("%w: signing certificate is outside its validity period", ErrInvalidCertificate)
	}

	hasRequiredSAN := false
	for _, dnsName := range leaf.DNSNames {
		if strings.EqualFold(dnsName, alexaSigningDNSName) {
			hasRequiredSAN = true
			break
		}
	}
	if !hasRequiredSAN {
		return verifiedCertificate{}, fmt.Errorf("%w: required signing SAN is absent", ErrInvalidCertificate)
	}

	if roots == nil {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			return verifiedCertificate{}, fmt.Errorf("%w: load system roots", ErrInvalidCertificate)
		}
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range certificates[1:] {
		intermediates.AddCert(certificate)
	}
	chains, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil || len(chains) == 0 {
		return verifiedCertificate{}, fmt.Errorf("%w: chain validation failed", ErrInvalidCertificate)
	}

	validFrom := chains[0][0].NotBefore
	validUntil := chains[0][0].NotAfter
	for _, certificate := range chains[0][1:] {
		if certificate.NotBefore.After(validFrom) {
			validFrom = certificate.NotBefore
		}
		if certificate.NotAfter.Before(validUntil) {
			validUntil = certificate.NotAfter
		}
	}
	return verifiedCertificate{leaf: leaf, validFrom: validFrom, validUntil: validUntil}, nil
}

func verifyTimestamp(rawTimestamp string, now time.Time) error {
	timestamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(rawTimestamp))
	if err != nil {
		return ErrInvalidTimestamp
	}
	difference := now.Sub(timestamp)
	if difference < -requestTimestampTolerance || difference > requestTimestampTolerance {
		return ErrInvalidTimestamp
	}
	return nil
}
