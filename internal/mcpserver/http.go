package mcpserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/progress"
)

// HTTPOptions configure the Streamable HTTP transport.
type HTTPOptions struct {
	Addr              string // host:port to listen on
	Token             string // bearer token every request must carry
	CertFile, KeyFile string // TLS; required unless Addr is a loopback address
}

const (
	// HTTPPath is where the server answers.
	HTTPPath = "/mcp"
	// sessionTimeout closes sessions that sent nothing for this long.
	sessionTimeout = 30 * time.Minute
)

// Check validates the options: a token, and TLS off loopback, so that the
// token and cluster credentials never cross a network in clear text.
func (o HTTPOptions) Check() error {
	if len(o.Token) < 16 {
		return errors.New("the bearer token must have at least 16 characters")
	}
	host, _, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", o.Addr, err)
	}
	if (o.CertFile == "") != (o.KeyFile == "") {
		return errors.New("set both --tls-cert and --tls-key")
	}
	if ip := net.ParseIP(host); o.CertFile == "" && (ip == nil || !ip.IsLoopback()) && host != "localhost" {
		return fmt.Errorf("listening on %s needs TLS (--tls-cert, --tls-key); without it only a loopback address is allowed", host)
	}
	return nil
}

// NewHTTPHandler serves s over Streamable HTTP at HTTPPath to clients that send
// the bearer token.
func NewHTTPHandler(s *mcp.Server, token string) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{SessionTimeout: sessionTimeout})
	mux := http.NewServeMux()
	mux.Handle(HTTPPath, requireToken(token, http.NewCrossOriginProtection().Handler(h)))
	return mux
}

// requireToken rejects requests without "Authorization: Bearer <token>".
func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="proxbase"`)
			http.Error(w, "missing or wrong bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ServeHTTP serves the tools over Streamable HTTP until ctx ends.
func ServeHTTP(ctx context.Context, version string, log progress.Sink, opts Options, h HTTPOptions) error {
	if err := h.Check(); err != nil {
		return err
	}
	s, err := New(version, log, opts)
	if err != nil {
		return err
	}
	l, err := net.Listen("tcp", h.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: NewHTTPHandler(s, h.Token), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	scheme := "http"
	if h.CertFile != "" {
		scheme = "https"
	}
	if log != nil {
		log.Logf("MCP server on %s://%s%s", scheme, l.Addr(), HTTPPath)
	}
	if h.CertFile != "" {
		err = srv.ServeTLS(l, h.CertFile, h.KeyFile)
	} else {
		err = srv.Serve(l)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
