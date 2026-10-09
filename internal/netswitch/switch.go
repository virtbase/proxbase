// Package netswitch is a learning L2 switch over unix datagram sockets, one per
// network. QEMU connects with -netdev dgram (local + remote unix paths); only
// peers known up front are accepted and frames are never echoed to the sender.
package netswitch

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

const writeTimeout = 5 * time.Millisecond

type Switch struct {
	conn  *net.UnixConn
	ports map[string]*net.UnixAddr // by socket path
	mu    sync.Mutex
	fdb   map[[6]byte]*net.UnixAddr
}

// Listen binds the switch socket at path; peers are the node-side socket paths.
func Listen(path string, peers []string) (*Switch, error) {
	_ = os.Remove(path)
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	_ = conn.SetReadBuffer(4 << 20)
	s := &Switch{conn: conn, ports: map[string]*net.UnixAddr{}, fdb: map[[6]byte]*net.UnixAddr{}}
	for _, p := range peers {
		s.ports[p] = &net.UnixAddr{Name: p, Net: "unixgram"}
	}
	return s, nil
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
		in, ok := s.ports[src.Name]
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
	s.mu.Unlock()
	if known && dst[0]&1 == 0 {
		if out != in {
			s.send(frame, out)
		}
		return
	}
	for _, p := range s.ports {
		if p != in {
			s.send(frame, p)
		}
	}
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
