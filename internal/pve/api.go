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
	"time"
)

type Client struct {
	base   string
	http   *http.Client
	ticket string
	csrf   string
	token  string // "user@realm!id=secret"
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
		http: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsCfg}},
	}
}

// Login gets a ticket for user (e.g. root@pam).
func (c *Client) Login(ctx context.Context, user, password string) error {
	var out struct {
		Ticket string `json:"ticket"`
		CSRF   string `json:"CSRFPreventionToken"`
	}
	if err := c.Do(ctx, http.MethodPost, "/access/ticket", url.Values{"username": {user}, "password": {password}}, &out); err != nil {
		return err
	}
	c.ticket, c.csrf = out.Ticket, out.CSRF
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

func (c *Client) Do(ctx context.Context, method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	switch {
	case c.token != "":
		req.Header.Set("Authorization", "PVEAPIToken="+c.token)
	case c.ticket != "":
		req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: c.ticket}) //nolint:gosec // request cookie, attributes do not apply
		if method != http.MethodGet {
			req.Header.Set("CSRFPreventionToken", c.csrf)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
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
		return &APIError{Status: resp.StatusCode, Msg: msg}
	}
	if out == nil {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	if len(env.Data) == 0 || bytes.Equal(env.Data, []byte("null")) {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// TaskDone reports whether a task has stopped and, if it failed, its error with the log tail.
func (c *Client) TaskDone(ctx context.Context, node, upid string) (bool, error) {
	var st struct {
		Status     string `json:"status"`
		ExitStatus string `json:"exitstatus"`
	}
	if err := c.Get(ctx, "/nodes/"+node+"/tasks/"+url.PathEscape(upid)+"/status", &st); err != nil || st.Status != "stopped" {
		return false, nil
	}
	if st.ExitStatus == "OK" || strings.HasPrefix(st.ExitStatus, "WARNINGS") {
		return true, nil
	}
	var lines []struct {
		T string `json:"t"`
	}
	_ = c.Get(ctx, "/nodes/"+node+"/tasks/"+url.PathEscape(upid)+"/log?limit=20", &lines)
	var log []string
	for _, l := range lines {
		log = append(log, "  "+l.T)
	}
	return true, fmt.Errorf("task failed: %s\n%s", st.ExitStatus, strings.Join(log, "\n"))
}

// WaitTask waits for a UPID to finish and returns an error unless it ended OK.
func (c *Client) WaitTask(ctx context.Context, node, upid string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		var st struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		err := c.Get(ctx, "/nodes/"+node+"/tasks/"+url.PathEscape(upid)+"/status", &st)
		if err == nil && st.Status == "stopped" {
			if st.ExitStatus != "OK" && !strings.HasPrefix(st.ExitStatus, "WARNINGS") {
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

// SetToken switches the client to API token auth ("user@realm!id=secret").
func (c *Client) SetToken(token string) { c.token = token }
