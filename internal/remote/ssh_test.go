package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// A port forward that accepts but never answers (a booting guest behind QEMU
// user networking) must not block Dial past its deadline.
func TestSSH_DialStalledHandshake(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		// Hold connections open without sending a banner until the listener closes.
		var conns []net.Conn
		defer func() {
			for _, c := range conns {
				c.Close()
			}
		}()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	s := &SSH{Port: l.Addr().(*net.TCPAddr).Port, Signer: signer}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	if _, err := s.Dial(ctx); err == nil {
		t.Fatal("Dial succeeded against a server that never answers")
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("Dial took %s; the handshake must honour the context deadline", d)
	}
}

func TestQuote(t *testing.T) {
	for _, s := range []string{"plain", "with space", "it's", `"double"`, "$HOME `id` $(id)", "a\nb", "-- -x", ""} {
		t.Run(s, func(t *testing.T) {
			out, err := exec.Command("sh", "-c", "printf %s "+Quote(s)).Output()
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != s {
				t.Errorf("sh printf %s = %q, want %q", Quote(s), out, s)
			}
		})
	}
}
