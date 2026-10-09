// Package progress carries progress events of long operations (create, start,
// node add, ...) to a sink: human-readable text, JSON lines or an MCP client.
package progress

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Event types.
const (
	TypeStep  = "step"  // a step of an operation finished or started
	TypeLog   = "log"   // any other message
	TypeDone  = "done"  // the command succeeded
	TypeError = "error" // the command failed
)

// Event is one progress report. Elapsed is seconds since the operation started.
type Event struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Cluster string    `json:"cluster,omitempty"`
	Node    string    `json:"node,omitempty"`
	Message string    `json:"message,omitempty"`
	Elapsed float64   `json:"elapsed,omitempty"`
	Error   string    `json:"error,omitempty"`
	Hint    string    `json:"hint,omitempty"`
	Log     string    `json:"log,omitempty"` // file or directory with details
}

// Sink receives events. It must be safe for concurrent use.
type Sink func(Event)

// Logf sends a log event.
func (s Sink) Logf(format string, a ...any) {
	if s != nil {
		s(Event{Time: time.Now(), Type: TypeLog, Message: fmt.Sprintf(format, a...)})
	}
}

// Discard drops all events.
func Discard(Event) {}

// Text writes events as lines for humans, steps prefixed with the elapsed time.
func Text(w io.Writer) Sink {
	var mu sync.Mutex
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Type {
		case TypeStep:
			fmt.Fprintf(w, "[%5.0fs] %s\n", e.Elapsed, e.Message)
		case TypeError:
			fmt.Fprintln(w, "Error:", e.Error)
			if e.Hint != "" {
				fmt.Fprintf(w, "\n%s\n", e.Hint)
			}
		default:
			if e.Message != "" {
				fmt.Fprintln(w, e.Message)
			}
		}
	}
}

// JSON writes one JSON object per event and line.
func JSON(w io.Writer) Sink {
	var mu sync.Mutex
	enc := json.NewEncoder(w)
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(e)
	}
}

// Tee sends every event to all sinks.
func Tee(sinks ...Sink) Sink {
	return func(e Event) {
		for _, s := range sinks {
			s(e)
		}
	}
}
