package controller

import (
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/model"
)

type stateSnapshot struct {
	revision uint64
	state    *model.State
	final    bool
}

// One writer owns streaming saves. A newer synchronous save (settings, close)
// invalidates queued snapshots and cannot be overwritten by an older turn.
type snapshotWriter struct {
	path     string
	mu       sync.Mutex
	revision atomic.Uint64
	pending  chan stateSnapshot
	done     chan struct{}
}

func (c *Controller) saveState() error {
	if c.writer != nil {
		return c.writer.save(c.State)
	}
	return model.Save(filepath.Join(c.Dir, "state.json"), c.State)
}

func (w *snapshotWriter) save(state *model.State) error {
	w.revision.Add(1)
	w.mu.Lock()
	defer w.mu.Unlock()
	return model.Save(w.path, state)
}

func (c *Controller) saveStream() {
	if c.writer == nil {
		w := &snapshotWriter{path: filepath.Join(c.Dir, "state.json"), pending: make(chan stateSnapshot, 1), done: make(chan struct{})}
		c.writer = w
		go func() {
			defer close(w.done)
			for snapshot := range w.pending {
				timer := time.NewTimer(time.Second)
			coalesce:
				for !snapshot.final {
					select {
					case newer, ok := <-w.pending:
						if !ok {
							timer.Stop()
							return // Close has already synchronously saved the final state.
						}
						snapshot = newer
					case <-timer.C:
						break coalesce
					}
				}
				timer.Stop()
				w.mu.Lock()
				var err error
				if snapshot.revision == w.revision.Load() {
					err = model.Save(w.path, snapshot.state)
				}
				w.mu.Unlock()
				if err != nil {
					c.post(func() {
						if snapshot.revision == w.revision.Load() {
							c.ReportError(err.Error())
						}
					})
				}
			}
		}()
	}
	w := c.writer
	snapshot := stateSnapshot{revision: w.revision.Add(1), state: cloneState(c.State), final: c.streamFinal}
	c.streamFinal = false
	// Replace only pending work; never wait for JSON encoding or fsync on the UI.
	select {
	case <-w.pending:
	default:
	}
	w.pending <- snapshot
}

// Strings are immutable; copy the mutable slices and pointer-owned records.
func cloneState(s *model.State) *model.State {
	result := *s
	result.Projects = slices.Clone(s.Projects)
	result.Settings.ModelFavorites = slices.Clone(s.Settings.ModelFavorites)
	result.Threads = make([]*model.Thread, len(s.Threads))
	for i, thread := range s.Threads {
		copy := *thread
		copy.Queue = slices.Clone(thread.Queue)
		copy.Turns = slices.Clone(thread.Turns)
		for j := range copy.Turns {
			copy.Turns[j].Items = slices.Clone(thread.Turns[j].Items)
			for k := range copy.Turns[j].Items {
				if detail := copy.Turns[j].Items[k].MCP; detail != nil {
					value := *detail
					copy.Turns[j].Items[k].MCP = &value
				}
			}
		}
		result.Threads[i] = &copy
	}
	return &result
}
