// Package netswitch is a learning L2 switch over unix datagram sockets, one per
// network. QEMU connects with -netdev dgram (local + remote unix paths); only
// peers known up front are accepted and frames are never echoed to the sender.
package netswitch

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"os"
	"sync"
	"time"
)

const writeTimeout = 5 * time.Millisecond

type Switch struct {
	conn   *net.UnixConn
	ports  map[string]*net.UnixAddr // by socket path
	mu     sync.Mutex
	fdb    map[[6]byte]*net.UnixAddr
	policy Policy
}

// Policy degrades forwarding for fault injection. The zero value forwards everything.
type Policy struct {
	// Block reports whether frames from one peer socket to another are dropped.
	Block func(from, to string) bool
	Delay time.Duration // added to every frame
	Loss  float64       // fraction of frames dropped, 0..1
}

// SetPolicy replaces the forwarding policy.
func (s *Switch) SetPolicy(p Policy) {
	s.mu.Lock()
	s.policy = p
	s.mu.Unlock()
}

// Listen binds the switch socket at path; peers are the node-side socket paths.
func Listen(path string, peers []string) (*Switch, error) {
	_ = os.Remove(path)
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	_ = conn.SetReadBuffer(4 << 20)
	s := &Switch{conn: conn, fdb: map[[6]byte]*net.UnixAddr{}}
	s.SetPeers(peers)
	return s, nil
}

// SetPeers replaces the accepted peers (nodes added or removed).
func (s *Switch) SetPeers(peers []string) {
	ports := map[string]*net.UnixAddr{}
	for _, p := range peers {
		ports[p] = &net.UnixAddr{Name: p, Net: "unixgram"}
	}
	s.mu.Lock()
	s.ports = ports
	s.fdb = map[[6]byte]*net.UnixAddr{}
	s.mu.Unlock()
}

// Serve forwards frames until ctx is cancelled.
func (s *Switch) Serve(ctx context.Context) error {
	go func() { <-ctx.Done(); s.conn.Close() }()
	buf := make([]byte, 65536)
	for {
		n, src, err := s.conn.ReadFromUnix(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if src == nil || n < 14 {
			continue
		}
		s.mu.Lock()
		in, ok := s.ports[src.Name]
		s.mu.Unlock()
		if !ok {
			continue
		}
		s.forward(buf[:n], in)
	}
}

func (s *Switch) forward(frame []byte, in *net.UnixAddr) {
	var dst, srcMAC [6]byte
	copy(dst[:], frame[0:6])
	copy(srcMAC[:], frame[6:12])
	s.mu.Lock()
	if srcMAC[0]&1 == 0 {
		s.fdb[srcMAC] = in
	}
	out, known := s.fdb[dst]
	ports, policy := s.ports, s.policy
	s.mu.Unlock()
	if known && dst[0]&1 == 0 {
		if out != in {
			s.deliver(policy, frame, in, out)
		}
		return
	}
	for _, p := range ports {
		if p != in {
			s.deliver(policy, frame, in, p)
		}
	}
}

// deliver applies the policy and sends; delayed frames are copied because the
// read buffer is reused.
func (s *Switch) deliver(p Policy, frame []byte, from, to *net.UnixAddr) {
	if p.Block != nil && p.Block(from.Name, to.Name) {
		return
	}
	if p.Loss > 0 && rand.Float64() < p.Loss { //nolint:gosec // simulated packet loss, not security relevant
		return
	}
	if p.Delay > 0 {
		f := append([]byte(nil), frame...)
		time.AfterFunc(p.Delay, func() { s.send(f, to) })
		return
	}
	s.send(frame, to)
}

// send drops the frame if the peer is gone or not reading.
func (s *Switch) send(frame []byte, to *net.UnixAddr) {
	_ = s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, _ = s.conn.WriteToUnix(frame, to)
}

func (s *Switch) Close() error {
	err := s.conn.Close()
	_ = os.Remove(s.conn.LocalAddr().String())
	return err
}
