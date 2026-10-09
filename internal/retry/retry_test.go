package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo(t *testing.T) {
	calls := 0
	err := Do(context.Background(), time.Second, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestDoTimeout(t *testing.T) {
	want := errors.New("still failing")
	if err := Do(context.Background(), 20*time.Millisecond, 5*time.Millisecond, func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("want last error, got %v", err)
	}
}

func TestDoPermanent(t *testing.T) {
	want := errors.New("fatal")
	calls := 0
	err := Do(context.Background(), time.Second, time.Millisecond, func() error { calls++; return Permanent(want) })
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestDoContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Do(ctx, time.Second, 10*time.Millisecond, func() error { return errors.New("x") }); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context error, got %v", err)
	}
}
