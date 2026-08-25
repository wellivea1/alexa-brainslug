package alexa

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testCertificateURL = "https://s3.amazonaws.com/echo.api/test-cert.pem"

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type signingFixture struct {
	now        time.Time
	skillID    string
	roots      *x509.CertPool
	chainPEM   []byte
	privateKey *rsa.PrivateKey
}

func TestRequestVerifierAcceptsValidRequestAndCachesCertificate(t *testing.T) {
	fixture := newSigningFixture(t, alexaSigningDNSName, time.Time{}, time.Time{})
	body, envelope := fixture.requestBody(t, fixture.now)
	signature := fixture.sign(t, body)

	var requests atomic.Int32
	client := certificateClient(fixture.chainPEM, http.StatusOK, &requests)
	verifier := NewRequestVerifier(fixture.config(client))

	for range 2 {
		request := signedRequest(t, body, signature, testCertificateURL)
		if err := verifier.Verify(request, body, envelope); err != nil {
			t.Fatalf("Verify() error = %v", err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("certificate requests = %d, want 1", got)
	}
}

func TestRequestVerifierRejectsInvalidRequests(t *testing.T) {
	fixture := newSigningFixture(t, alexaSigningDNSName, time.Time{}, time.Time{})

	t.Run("tampered body", func(t *testing.T) {
		body, envelope := fixture.requestBody(t, fixture.now)
		signature := fixture.sign(t, body)
		tampered := append(append([]byte(nil), body...), ' ')
		verifier := NewRequestVerifier(fixture.config(certificateClient(fixture.chainPEM, http.StatusOK, nil)))
		err := verifier.Verify(signedRequest(t, tampered, signature, testCertificateURL), tampered, envelope)
		if !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("Verify() error = %v", err)
		}
	})

	t.Run("stale timestamp", func(t *testing.T) {
		body, envelope := fixture.requestBody(t, fixture.now.Add(-requestTimestampTolerance-time.Second))
		signature := fixture.sign(t, body)
		verifier := NewRequestVerifier(fixture.config(certificateClient(fixture.chainPEM, http.StatusOK, nil)))
		err := verifier.Verify(signedRequest(t, body, signature, testCertificateURL), body, envelope)
		if !errors.Is(err, ErrInvalidTimestamp) {
			t.Errorf("Verify() error = %v", err)
		}
	})

	t.Run("future timestamp", func(t *testing.T) {
		body, envelope := fixture.requestBody(t, fixture.now.Add(requestTimestampTolerance+time.Second))
		signature := fixture.sign(t, body)
		verifier := NewRequestVerifier(fixture.config(certificateClient(fixture.chainPEM, http.StatusOK, nil)))
		err := verifier.Verify(signedRequest(t, body, signature, testCertificateURL), body, envelope)
		if !errors.Is(err, ErrInvalidTimestamp) {
			t.Errorf("Verify() error = %v", err)
		}
	})

	t.Run("wrong skill ID", func(t *testing.T) {
		body, envelope := fixture.requestBody(t, fixture.now)
		envelope.Session.Application.ApplicationID = "amzn1.ask.skill.someone-else"
		var requests atomic.Int32
		verifier := NewRequestVerifier(fixture.config(certificateClient(fixture.chainPEM, http.StatusOK, &requests)))
		err := verifier.Verify(signedRequest(t, body, fixture.sign(t, body), testCertificateURL), body, envelope)
		if err == nil {
			t.Fatal("Verify() accepted a foreign skill ID")
		}
		if got := requests.Load(); got != 0 {
			t.Errorf("certificate requests = %d, want 0", got)
		}
	})

	t.Run("missing signature", func(t *testing.T) {
		body, envelope := fixture.requestBody(t, fixture.now)
		request := signedRequest(t, body, "", testCertificateURL)
		verifier := NewRequestVerifier(fixture.config(certificateClient(fixture.chainPEM, http.StatusOK, nil)))
		if err := verifier.Verify(request, body, envelope); !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("Verify() error = %v", err)
		}
	})

	t.Run("untrusted chain", func(t *testing.T) {
		body, envelope := fixture.requestBody(t, fixture.now)
		config := fixture.config(certificateClient(fixture.chainPEM, http.StatusOK, nil))
		config.RootCAs = x509.NewCertPool()
		verifier := NewRequestVerifier(config)
		err := verifier.Verify(signedRequest(t, body, fixture.sign(t, body), testCertificateURL), body, envelope)
		if !errors.Is(err, ErrInvalidCertificate) {
			t.Errorf("Verify() error = %v", err)
		}
	})

	t.Run("wrong signing SAN", func(t *testing.T) {
		wrongSAN := newSigningFixture(t, "not-echo-api.amazon.com", time.Time{}, time.Time{})
		body, envelope := wrongSAN.requestBody(t, wrongSAN.now)
		verifier := NewRequestVerifier(wrongSAN.config(certificateClient(wrongSAN.chainPEM, http.StatusOK, nil)))
		err := verifier.Verify(signedRequest(t, body, wrongSAN.sign(t, body), testCertificateURL), body, envelope)
		if !errors.Is(err, ErrInvalidCertificate) {
			t.Errorf("Verify() error = %v", err)
		}
	})

	t.Run("expired signing certificate", func(t *testing.T) {
		expired := newSigningFixture(
			t,
			alexaSigningDNSName,
			fixture.now.Add(-2*time.Hour),
			fixture.now.Add(-time.Hour),
		)
		body, envelope := expired.requestBody(t, expired.now)
		verifier := NewRequestVerifier(expired.config(certificateClient(expired.chainPEM, http.StatusOK, nil)))
		err := verifier.Verify(signedRequest(t, body, expired.sign(t, body), testCertificateURL), body, envelope)
		if !errors.Is(err, ErrInvalidCertificate) {
			t.Errorf("Verify() error = %v", err)
		}
	})

	t.Run("certificate HTTP failure", func(t *testing.T) {
		body, envelope := fixture.requestBody(t, fixture.now)
		verifier := NewRequestVerifier(fixture.config(certificateClient(nil, http.StatusServiceUnavailable, nil)))
		err := verifier.Verify(signedRequest(t, body, fixture.sign(t, body), testCertificateURL), body, envelope)
		if !errors.Is(err, ErrCertificateFetch) {
			t.Errorf("Verify() error = %v", err)
		}
	})

	t.Run("oversized certificate response", func(t *testing.T) {
		body, envelope := fixture.requestBody(t, fixture.now)
		oversized := bytes.Repeat([]byte{'x'}, certificateResponseLimit+1)
		verifier := NewRequestVerifier(fixture.config(certificateClient(oversized, http.StatusOK, nil)))
		err := verifier.Verify(signedRequest(t, body, fixture.sign(t, body), testCertificateURL), body, envelope)
		if !errors.Is(err, ErrCertificateFetch) {
			t.Errorf("Verify() error = %v", err)
		}
	})
}

func TestNormalizeCertificateURL(t *testing.T) {
	valid := []struct {
		raw  string
		want string
	}{
		{
			"https://s3.amazonaws.com/echo.api/echo-api-cert.pem",
			"https://s3.amazonaws.com/echo.api/echo-api-cert.pem",
		},
		{
			"HTTPS://S3.AMAZONAWS.COM:443/echo.api/../echo.api//echo-api-cert.pem#fragment",
			"https://s3.amazonaws.com/echo.api/echo-api-cert.pem",
		},
	}
	for _, test := range valid {
		got, err := normalizeCertificateURL(test.raw)
		if err != nil {
			t.Errorf("normalizeCertificateURL(%q) error = %v", test.raw, err)
			continue
		}
		if got != test.want {
			t.Errorf("normalizeCertificateURL(%q) = %q, want %q", test.raw, got, test.want)
		}
	}

	invalid := []string{
		"",
		"http://s3.amazonaws.com/echo.api/cert.pem",
		"https://notamazon.com/echo.api/cert.pem",
		"https://s3.amazonaws.com/EcHo.aPi/cert.pem",
		"https://s3.amazonaws.com/invalid.path/cert.pem",
		"https://s3.amazonaws.com:563/echo.api/cert.pem",
		"https://user@s3.amazonaws.com/echo.api/cert.pem",
	}
	for _, raw := range invalid {
		if _, err := normalizeCertificateURL(raw); !errors.Is(err, ErrInvalidCertificateURL) {
			t.Errorf("normalizeCertificateURL(%q) error = %v", raw, err)
		}
	}
}

func TestVerifyRequestCanBeDisabledForControlledTesting(t *testing.T) {
	envelope := &RequestEnvelope{
		Session: Session{Application: Application{ApplicationID: "amzn1.ask.skill.test"}},
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", http.NoBody)
	if err := VerifyRequest(request, nil, envelope, VerificationConfig{
		VerifyRequests: false,
		AllowedSkillID: "amzn1.ask.skill.test",
	}); err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}
}

func newSigningFixture(t *testing.T, dnsName string, notBefore, notAfter time.Time) signingFixture {
	t.Helper()

	now := time.Date(2026, time.August, 25, 16, 0, 0, 0, time.UTC)
	if notBefore.IsZero() {
		notBefore = now.Add(-time.Hour)
	}
	if notAfter.IsZero() {
		notAfter = now.Add(time.Hour)
	}

	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate root key: %v", err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Brainslug Test Root"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create root certificate: %v", err)
	}
	rootCertificate, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatalf("parse root certificate: %v", err)
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, rootCertificate, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}

	chainPEM := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})...,
	)
	roots := x509.NewCertPool()
	roots.AddCert(rootCertificate)

	return signingFixture{
		now:        now,
		skillID:    "amzn1.ask.skill.test",
		roots:      roots,
		chainPEM:   chainPEM,
		privateKey: leafKey,
	}
}

func (f signingFixture) config(client *http.Client) VerificationConfig {
	return VerificationConfig{
		VerifyRequests: true,
		AllowedSkillID: f.skillID,
		HTTPClient:     client,
		RootCAs:        f.roots,
		Now: func() time.Time {
			return f.now
		},
	}
}

func (f signingFixture) requestBody(t *testing.T, timestamp time.Time) ([]byte, *RequestEnvelope) {
	t.Helper()
	envelope := &RequestEnvelope{
		Version: "1.0",
		Session: Session{
			SessionID:   "session-1",
			Application: Application{ApplicationID: f.skillID},
			User:        User{UserID: "user-1"},
		},
		Request: RequestPayload{
			Type:      requestTypeLaunch,
			RequestID: "request-1",
			Timestamp: timestamp.Format(time.RFC3339),
		},
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body, envelope
}

func (f signingFixture) sign(t *testing.T, body []byte) string {
	t.Helper()
	digest := sha256.Sum256(body)
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign request: %v", err)
	}
	return base64.StdEncoding.EncodeToString(signature)
}

func signedRequest(t *testing.T, body []byte, signature, certificateURL string) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", bytes.NewReader(body))
	request.Header.Set(signatureCertChainURLHeader, certificateURL)
	request.Header.Set(signature256Header, signature)
	return request
}

func certificateClient(body []byte, status int, requests *atomic.Int32) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if requests != nil {
			requests.Add(1)
		}
		if !strings.HasPrefix(request.URL.String(), "https://s3.amazonaws.com/echo.api/") {
			return nil, errors.New("unexpected certificate URL")
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    request,
		}, nil
	})}
}
