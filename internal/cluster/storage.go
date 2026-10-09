package cluster

import (
	"context"

	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/storage"
)

// setupStorage runs every storage backend's (idempotent) setup.
func (c *Cluster) setupStorage(ctx context.Context) error {
	for _, b := range c.storages() {
		if err := b.Setup(ctx, host{c}); err != nil {
			return err
		}
	}
	return nil
}

// waitStorage waits for backends that need time after the nodes started.
func (c *Cluster) waitStorage(ctx context.Context) error {
	for _, b := range c.storages() {
		if w, ok := b.(storage.Waiter); ok {
			if err := w.Wait(ctx, host{c}); err != nil {
				return err
			}
		}
	}
	return nil
}

// prepareStop lets backends act before the running nodes shut down.
func (c *Cluster) prepareStop(ctx context.Context, running []string) {
	for _, b := range c.storages() {
		if p, ok := b.(storage.StopPreparer); ok {
			p.BeforeStop(ctx, host{c}, running)
		}
	}
}

// storageHealth returns the health reported by backends that have one.
func (c *Cluster) storageHealth(ctx context.Context, api *pve.Client) string {
	for _, b := range c.storages() {
		if r, ok := b.(storage.HealthReporter); ok {
			return r.Health(ctx, api)
		}
	}
	return ""
}
