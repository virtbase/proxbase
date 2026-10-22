// Package pve talks to Proxmox VE nodes: a small REST client and an SSH runner.
package pve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client is safe for concurrent use.
type Client struct {
	base  string
	http  *http.Client
	token string // "user@realm!id=secret"; set before use

	mu             sync.Mutex
	ticket, csrf   string
	user, password string // for logging in again
}

// Fingerprint returns the colon-separated SHA-256 fingerprint of a PEM certificate,
// as expected by POST /cluster/config/join.
func Fingerprint(certPEM string) (string, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return "", errors.New("no PEM certificate")
	}
	return fingerprintDER(block.Bytes), nil
}

func fingerprintDER(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// NewClient talks to https://127.0.0.1:port and accepts only the certificate with
// the given fingerprint (read from the node over SSH).
func NewClient(port int, fingerprint string) *Client {
	return newClient(port, func(raw [][]byte) error {
		if fp := fingerprintDER(raw[0]); fp != fingerprint {
			return fmt.Errorf("certificate fingerprint mismatch: got %s", fp)
		}
		return nil
	})
}

// NewClientCA verifies the node certificate against the cluster CA. The host name
// is not checked: nodes are reached through 127.0.0.1 port forwards.
func NewClientCA(port int, roots *x509.CertPool) *Client {
	return newClient(port, func(raw [][]byte) error {
		cert, err := x509.ParseCertificate(raw[0])
		if err != nil {
			return err
		}
		_, err = cert.Verify(x509.VerifyOptions{Roots: roots})
		return err
	})
}

func newClient(port int, verify func(raw [][]byte) error) *Client {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // verified by VerifyConnection, which also runs on resumed sessions
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no certificate")
			}
			raw := make([][]byte, len(cs.PeerCertificates))
			for i, c := range cs.PeerCertificates {
				raw[i] = c.Raw
			}
			return verify(raw)
		},
	}
	return &Client{
		base: fmt.Sprintf("https://127.0.0.1:%d/api2/json", port),
		http: &http.Client{
			Timeout:   60 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg, IdleConnTimeout: 30 * time.Second},
		},
	}
}

// Login gets a ticket for user (e.g. root@pam).
// The client logs in again by itself when the ticket is rejected.
func (c *Client) Login(ctx context.Context, user, password string) error {
	var out struct {
		Ticket string `json:"ticket"`
		CSRF   string `json:"CSRFPreventionToken"`
	}
	if _, err := c.do(ctx, http.MethodPost, "/access/ticket", url.Values{"username": {user}, "password": {password}}, &out); err != nil {
		return err
	}
	c.mu.Lock()
	c.ticket, c.csrf, c.user, c.password = out.Ticket, out.CSRF, user, password
	c.mu.Unlock()
	return nil
}

// APIError is a non-2xx response.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Msg) }

func IsStatus(err error, code int) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == code
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) Post(ctx context.Context, path string, form url.Values, out any) error {
	return c.Do(ctx, http.MethodPost, path, form, out)
}

func (c *Client) Put(ctx context.Context, path string, form url.Values, out any) error {
	return c.Do(ctx, http.MethodPut, path, form, out)
}

func (c *Client) Delete(ctx context.Context, path string) error {
	return c.Do(ctx, http.MethodDelete, path, nil, nil)
}

// Do sends a request and decodes the data of the response into out. A request
// with a ticket that the node rejects (401) is sent once more after a new login:
// right after a node joined a cluster, tickets can briefly fail to verify while
// the cluster's auth key reaches it.
func (c *Client) Do(ctx context.Context, method, path string, form url.Values, out any) error {
	_, err := c.doRelogin(ctx, method, path, form, out)
	return err
}

// GetPage is Get for paged lists: it also returns the number of entries in
// all pages, which the API reports next to the data.
func (c *Client) GetPage(ctx context.Context, path string, out any) (total int, err error) {
	return c.doRelogin(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) doRelogin(ctx context.Context, method, path string, form url.Values, out any) (int, error) {
	total, err := c.do(ctx, method, path, form, out)
	c.mu.Lock()
	user, password := c.user, c.password
	c.mu.Unlock()
	if !IsStatus(err, http.StatusUnauthorized) || c.token != "" || user == "" {
		return total, err
	}
	if lerr := c.Login(ctx, user, password); lerr != nil {
		return 0, errors.Join(err, lerr)
	}
	return c.do(ctx, method, path, form, out)
}

// do sends one request and returns the total the response reports, if any.
func (c *Client) do(ctx context.Context, method, path string, form url.Values, out any) (int, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return 0, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c.mu.Lock()
	ticket, csrf := c.ticket, c.csrf
	c.mu.Unlock()
	switch {
	case c.token != "":
		req.Header.Set("Authorization", "PVEAPIToken="+c.token)
	case ticket != "":
		req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket}) //nolint:gosec // request cookie, attributes do not apply
		if method != http.MethodGet {
			req.Header.Set("CSRFPreventionToken", csrf)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(resp.Status)
		var e struct {
			Message string            `json:"message"`
			Errors  map[string]string `json:"errors"`
		}
		if json.Unmarshal(b, &e) == nil {
			if e.Message != "" {
				msg = strings.TrimSpace(e.Message)
			}
			for k, v := range e.Errors {
				msg += fmt.Sprintf("; %s: %s", k, strings.TrimSpace(v))
			}
		}
		return 0, &APIError{Status: resp.StatusCode, Msg: msg}
	}
	if out == nil {
		return 0, nil
	}
	var env struct {
		Data  json.RawMessage `json:"data"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if len(env.Data) == 0 || bytes.Equal(env.Data, []byte("null")) {
		return env.Total, nil
	}
	return env.Total, json.Unmarshal(env.Data, out)
}

// TaskDone reports whether a task has stopped and, if it failed, its error with the log tail.
func (c *Client) TaskDone(ctx context.Context, node, upid string) (bool, error) {
	st, err := c.TaskStatus(ctx, node, upid)
	if err != nil || !st.Stopped() {
		return false, nil
	}
	if st.OK() {
		return true, nil
	}
	lines, _, _ := c.TaskLogTail(ctx, node, upid, 20)
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return true, fmt.Errorf("task failed: %s\n%s", st.ExitStatus, strings.Join(lines, "\n"))
}

// WaitTask waits for a UPID to finish and returns an error unless it ended OK.
func (c *Client) WaitTask(ctx context.Context, node, upid string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		st, err := c.TaskStatus(ctx, node, upid)
		if err == nil && st.Stopped() {
			if !st.OK() {
				return fmt.Errorf("task %s failed: %s", upid, st.ExitStatus)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(fmt.Errorf("task %s: %w", upid, ctx.Err()), err)
		case <-time.After(time.Second):
		}
	}
}

// SetToken switches the client to API token auth ("user@realm!id=secret"). Call
// it before using the client.
func (c *Client) SetToken(token string) { c.token = token }

// WithToken returns a client for the same node that uses an API token.
func (c *Client) WithToken(token string) *Client {
	return &Client{base: c.base, http: c.http, token: token}
}
