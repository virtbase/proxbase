package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHTTPOptionsCheck(t *testing.T) {
	token := strings.Repeat("t", 32)
	tests := []struct {
		name string
		opts HTTPOptions
		want string // "" for valid
	}{
		{"loopback", HTTPOptions{Addr: "127.0.0.1:8765", Token: token}, ""},
		{"localhost", HTTPOptions{Addr: "localhost:8765", Token: token}, ""},
		{"ipv6 loopback", HTTPOptions{Addr: "[::1]:8765", Token: token}, ""},
		{"network with TLS", HTTPOptions{Addr: "0.0.0.0:8765", Token: token, CertFile: "c", KeyFile: "k"}, ""},
		{"network without TLS", HTTPOptions{Addr: "0.0.0.0:8765", Token: token}, "needs TLS"},
		{"all interfaces without TLS", HTTPOptions{Addr: ":8765", Token: token}, "needs TLS"},
		{"short token", HTTPOptions{Addr: "127.0.0.1:8765", Token: "abc"}, "at least 16"},
		{"half TLS", HTTPOptions{Addr: "127.0.0.1:8765", Token: token, CertFile: "c"}, "both"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Check()
			if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Errorf("Check = %v, want %q", err, tt.want)
			}
		})
	}
}

// bearer adds an Authorization header to every request.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestHTTPHandler(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	token := strings.Repeat("test-", 8) // a made-up token for the test
	s, err := New("test", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewHTTPHandler(s, token))
	defer srv.Close()

	resp, err := http.Post(srv.URL+HTTPPath, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("without token: %s %q", resp.Status, resp.Header.Get("WWW-Authenticate"))
	}

	connect := func(tok string) (*mcp.ClientSession, error) {
		tr := &mcp.StreamableClientTransport{Endpoint: srv.URL + HTTPPath, HTTPClient: &http.Client{Transport: bearer{tok}}}
		return mcp.NewClient(&mcp.Implementation{Name: "remote"}, nil).Connect(context.Background(), tr, nil)
	}
	if cs, err := connect("wrong-token-wrong-token"); err == nil {
		cs.Close()
		t.Error("connected with a wrong token")
	}
	cs, err := connect(token)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cluster_list"})
	if err != nil || res.IsError {
		t.Fatalf("cluster_list over HTTP: %v %+v", err, res)
	}
}
