// Package storage is the contract between the cluster orchestration and the data
// storage backends (packages zfs and ceph). A backend implements Backend and any
// of the optional interfaces it needs.
package storage

import (
	"context"

	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/pve"
	"github.com/virtbase/proxbase/internal/remote"
)

// Host is what a backend may use of the cluster it configures.
type Host interface {
	// Nodes returns the current cluster nodes; the first one anchors cluster-wide calls.
	Nodes() []config.Node
	// API returns a root@pam client for a node.
	API(ctx context.Context, node string) (*pve.Client, error)
	// SSH connects to a node as root, waiting briefly for it to accept logins.
	SSH(ctx context.Context, node string) (*remote.SSH, error)
	// Step reports progress.
	Step(format string, a ...any)
}

// Backend sets up one kind of storage. Setup must be idempotent: it runs on
// create, after nodes were added and after a node was removed.
type Backend interface {
	Setup(ctx context.Context, h Host) error
}

// Waiter is implemented by backends that need time after the nodes started.
type Waiter interface {
	Wait(ctx context.Context, h Host) error
}

// StopPreparer is implemented by backends that need to act before nodes shut down.
// It is best effort and gets the running nodes.
type StopPreparer interface {
	BeforeStop(ctx context.Context, h Host, running []string)
}

// NodeRemover is implemented by backends that keep data or daemons on nodes.
type NodeRemover interface {
	// CheckRemove fails if the remaining nodes cannot hold the backend's data.
	CheckRemove(remaining int, force bool) error
	// RemoveNode takes node out of the backend while it is still a cluster member.
	RemoveNode(ctx context.Context, h Host, node string, force bool) error
}

// HealthReporter is implemented by backends with a health state worth showing.
type HealthReporter interface {
	Health(ctx context.Context, api *pve.Client) string
}
