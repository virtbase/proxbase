// Package retry repeats an operation until it succeeds, a timeout passes or the
// context ends.
package retry

import (
	"context"
	"errors"
	"time"
)

type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent marks an error that ends Do immediately.
func Permanent(err error) error { return permanent{err} }

// Do calls f until it returns nil. It gives up when timeout has passed, when f
// returns a Permanent error or when ctx ends, and returns the last error of f
// (unwrapped from Permanent) or the context error.
func Do(ctx context.Context, timeout, interval time.Duration, f func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := f()
		if err == nil {
			return nil
		}
		var p permanent
		if errors.As(err, &p) {
			return p.err
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
