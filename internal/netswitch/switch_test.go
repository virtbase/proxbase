package netswitch

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

type peer struct {
	conn *net.UnixConn
	mac  []byte
}

func frame(dst, src []byte, payload string) []byte {
	f := append(append(append([]byte{}, dst...), src...), 0x08, 0x00)
	return append(f, payload...)
}

func (p *peer) recv(t *testing.T) []byte {
	t.Helper()
	buf := make([]byte, 2048)
	_ = p.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, err := p.conn.Read(buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

func TestSwitch(t *testing.T) {
	dir := t.TempDir()
	swPath := filepath.Join(dir, "sw.sock")
	var paths []string
	for _, n := range []string{"a", "b", "c"} {
		paths = append(paths, filepath.Join(dir, n+".sock"))
	}
	sw, err := Listen(swPath, paths)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- sw.Serve(ctx) }()
	defer func() { cancel(); <-done; sw.Close() }()

	remote := &net.UnixAddr{Name: swPath, Net: "unixgram"}
	peers := make([]*peer, 3)
	for i, p := range paths {
		c, err := net.DialUnix("unixgram", &net.UnixAddr{Name: p, Net: "unixgram"}, remote)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		peers[i] = &peer{conn: c, mac: []byte{0x52, 0x54, 0, 0, 0, byte(i + 1)}}
	}
	bcast := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

	// Broadcast floods to everyone but the sender.
	f := frame(bcast, peers[0].mac, "hello")
	_, _ = peers[0].conn.Write(f)
	for i, p := range peers[1:] {
		if got := p.recv(t); !bytes.Equal(got, f) {
			t.Fatalf("peer %d: broadcast not received", i+1)
		}
	}
	if got := peers[0].recv(t); got != nil {
		t.Fatal("broadcast echoed to sender")
	}

	// b answers a: a's MAC is learned, so only a receives it.
	f = frame(peers[0].mac, peers[1].mac, "reply")
	_, _ = peers[1].conn.Write(f)
	if got := peers[0].recv(t); !bytes.Equal(got, f) {
		t.Fatal("unicast not delivered")
	}
	if got := peers[2].recv(t); got != nil {
		t.Fatal("learned unicast flooded")
	}

	// Unknown unicast floods.
	f = frame([]byte{0x52, 0x54, 0, 0, 0, 9}, peers[1].mac, "unknown")
	_, _ = peers[1].conn.Write(f)
	if peers[0].recv(t) == nil || peers[2].recv(t) == nil {
		t.Fatal("unknown unicast not flooded")
	}

	// Frames from unknown sockets are ignored.
	stranger, err := net.DialUnix("unixgram", &net.UnixAddr{Name: filepath.Join(dir, "x.sock"), Net: "unixgram"}, remote)
	if err != nil {
		t.Fatal(err)
	}
	defer stranger.Close()
	_, _ = stranger.Write(frame(bcast, []byte{2, 0, 0, 0, 0, 1}, "intruder"))
	if peers[0].recv(t) != nil {
		t.Fatal("frame from unknown peer forwarded")
	}

	// Peers can be replaced at runtime: without c, a's broadcast only reaches b.
	sw.SetPeers(paths[:2])
	f = frame(bcast, peers[0].mac, "two peers")
	_, _ = peers[0].conn.Write(f)
	if peers[1].recv(t) == nil || peers[2].recv(t) != nil {
		t.Fatal("SetPeers not applied")
	}
	sw.SetPeers(paths)

	// A peer that went away does not block delivery to the others.
	peers[2].conn.Close()
	f = frame(bcast, peers[0].mac, "after close")
	_, _ = peers[0].conn.Write(f)
	if got := peers[1].recv(t); !bytes.Equal(got, f) {
		t.Fatal("delivery broken after peer left")
	}
}

func TestPolicy(t *testing.T) {
	dir := t.TempDir()
	swPath := filepath.Join(dir, "sw.sock")
	var paths []string
	for _, n := range []string{"a", "b", "c"} {
		paths = append(paths, filepath.Join(dir, n+".sock"))
	}
	sw, err := Listen(swPath, paths)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- sw.Serve(ctx) }()
	defer func() { cancel(); <-done; _ = sw.Close() }()
	remote := &net.UnixAddr{Name: swPath, Net: "unixgram"}
	peers := make([]*peer, 3)
	for i, p := range paths {
		c, err := net.DialUnix("unixgram", &net.UnixAddr{Name: p, Net: "unixgram"}, remote)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		peers[i] = &peer{conn: c, mac: []byte{0x52, 0x54, 0, 0, 0, byte(i + 1)}}
	}
	bcast := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

	// Partition: c is cut off from a and b.
	isolated := paths[2]
	sw.SetPolicy(Policy{Block: func(from, to string) bool { return (from == isolated) != (to == isolated) }})
	_, _ = peers[0].conn.Write(frame(bcast, peers[0].mac, "partitioned"))
	if peers[1].recv(t) == nil || peers[2].recv(t) != nil {
		t.Fatal("partition: b must receive, c must not")
	}
	_, _ = peers[2].conn.Write(frame(bcast, peers[2].mac, "from c"))
	if peers[0].recv(t) != nil || peers[1].recv(t) != nil {
		t.Fatal("partition: frames from c must not pass")
	}

	// Delay: frames arrive, but not before the delay.
	sw.SetPolicy(Policy{Delay: 80 * time.Millisecond})
	start := time.Now()
	_, _ = peers[0].conn.Write(frame(bcast, peers[0].mac, "delayed"))
	if peers[1].recv(t) == nil || time.Since(start) < 80*time.Millisecond {
		t.Fatal("delay: frame missing or early")
	}
	peers[2].recv(t)

	// Loss 1: everything is dropped.
	sw.SetPolicy(Policy{Loss: 1})
	_, _ = peers[0].conn.Write(frame(bcast, peers[0].mac, "lost"))
	if peers[1].recv(t) != nil {
		t.Fatal("loss: frame must be dropped")
	}

	// Cleared policy forwards again.
	sw.SetPolicy(Policy{})
	_, _ = peers[0].conn.Write(frame(bcast, peers[0].mac, "ok"))
	if peers[1].recv(t) == nil {
		t.Fatal("cleared policy must forward")
	}
}
