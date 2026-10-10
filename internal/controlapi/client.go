package controlapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wellivea1/alexa-brainslug/internal/control"
)

// Client is the gateway's mutually authenticated controller port.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient requires HTTPS, pinned trust, and a client certificate. Redirects
// cannot forward prompts or ownership assertions to another destination.
func NewClient(baseURL string, configuration *tls.Config) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return nil, errors.New("controller URL must be an HTTPS origin")
	}
	if configuration == nil || configuration.InsecureSkipVerify || configuration.RootCAs == nil || len(configuration.Certificates) == 0 {
		return nil, errors.New("verified mutual TLS is required")
	}
	tlsOptions := configuration.Clone()
	tlsOptions.MinVersion = tls.VersionTLS13
	transport := &http.Transport{TLSClientConfig: tlsOptions}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Transport: transport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("controller redirects are not permitted")
	}}}, nil
}

func principalQuery(owner control.Principal) string {
	values := url.Values{"namespace": {owner.Namespace}, "subject": {owner.Subject}}
	return "?" + values.Encode()
}

func (c *Client) call(ctx context.Context, method, path string, input, out any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		if len(data) > maxMessageBytes {
			return control.ErrInvalid
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Brainslug-Protocol", strconv.Itoa(control.ProtocolVersion))
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%w: controller connection failed", control.ErrUnavailable)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch response.StatusCode {
		case http.StatusForbidden:
			return control.ErrForbidden
		case http.StatusNotFound:
			return control.ErrNotFound
		case http.StatusConflict:
			return control.ErrConflict
		case http.StatusTooManyRequests:
			return control.ErrQueueFull
		case http.StatusBadRequest:
			return control.ErrInvalid
		default:
			return control.ErrUnavailable
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxMessageBytes+1))
	if err != nil {
		return control.ErrUnavailable
	}
	if len(data) > maxMessageBytes {
		return control.ErrInvalid
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return control.ErrUnavailable
	}
	return nil
}

// Submit returns only controller-confirmed saved acceptance.
func (c *Client) Submit(ctx context.Context, request control.Request) (*control.Job, error) {
	var job control.Job
	err := c.call(ctx, http.MethodPost, "/v1/jobs", request, &job)
	if errors.Is(err, control.ErrUnavailable) && ctx.Err() == nil {
		// Only acceptance has a durable ingress idempotency key. Never retry a
		// worker operation or fabricate a new key after losing the response.
		err = c.call(ctx, http.MethodPost, "/v1/jobs", request, &job)
	}
	return &job, err
}

// Get reads an owner-scoped task.
func (c *Client) Get(ctx context.Context, owner control.Principal, id string) (*control.Job, error) {
	var job control.Job
	err := c.call(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id)+principalQuery(owner), nil, &job)
	return &job, err
}

// List returns a bounded owner-scoped task page.
func (c *Client) List(ctx context.Context, owner control.Principal) ([]*control.Job, error) {
	var jobs []*control.Job
	err := c.call(ctx, http.MethodGet, "/v1/jobs"+principalQuery(owner), nil, &jobs)
	return jobs, err
}

// Catalog returns authorized host/project/conversation choices.
func (c *Client) Catalog(ctx context.Context, owner control.Principal) (control.Catalog, error) {
	var catalog control.Catalog
	err := c.call(ctx, http.MethodGet, "/v1/catalog"+principalQuery(owner), nil, &catalog)
	return catalog, err
}

// Events resumes progress after a confirmed event sequence.
func (c *Client) Events(ctx context.Context, owner control.Principal, id string, after int64) ([]control.Event, error) {
	var events []control.Event
	err := c.call(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id)+"/events"+principalQuery(owner)+"&after="+strconv.FormatInt(after, 10), nil, &events)
	return events, err
}

// Cancel reports confirmed cancellation or cancel_requested while acknowledgement is pending.
func (c *Client) Cancel(ctx context.Context, owner control.Principal, id string) (*control.Job, error) {
	var job control.Job
	err := c.call(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/cancel", map[string]any{fieldPrincipal: owner}, &job)
	return &job, err
}

// Approve responds to an exact pending action, without granting session-wide permissions.
func (c *Client) Approve(ctx context.Context, owner control.Principal, id, approvalID, decision string) (*control.Job, error) {
	var job control.Job
	err := c.call(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/approval", map[string]any{fieldPrincipal: owner, "approval_id": approvalID, "decision": decision}, &job)
	return &job, err
}

// Reply supplies an owner-scoped clarification with its own stable request ID.
func (c *Client) Reply(ctx context.Context, owner control.Principal, id string, request control.Request) (*control.Job, error) {
	request.Principal = owner
	var job control.Job
	err := c.call(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/reply", request, &job)
	return &job, err
}

// Close releases idle transport connections during gateway shutdown.
func (c *Client) Close() { c.http.CloseIdleConnections() }
