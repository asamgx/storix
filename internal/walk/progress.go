package walk

import (
	"sync/atomic"
	"time"
)

// Progress is a snapshot of the walk's counters.
type Progress struct {
	Dirs    uint64
	Files   uint64
	Bytes   uint64
	Errors  uint64
	Links   uint64
	Skipped uint64
	Current string
	Elapsed time.Duration
}

// Event is what a walk publishes on Options.Events.
type Event interface{ isEvent() }

// ProgressEvent is a periodic counter snapshot. It is dropped rather than
// queued when the consumer is busy.
type ProgressEvent struct{ Progress }

// DoneEvent is sent once, after Finalize, and is never dropped.
type DoneEvent struct {
	Tree *Tree
	Err  error
}

func (ProgressEvent) isEvent() {}
func (DoneEvent) isEvent()     {}

// counters are the walk's live statistics. Workers only ever add to them.
type counters struct {
	dirs    atomic.Uint64
	files   atomic.Uint64
	bytes   atomic.Uint64
	errors  atomic.Uint64
	links   atomic.Uint64
	skipped atomic.Uint64
	current atomic.Pointer[string]
}

func (c *counters) setCurrent(path string) { c.current.Store(&path) }

func (c *counters) snapshot(started time.Time) Progress {
	p := Progress{
		Dirs:    c.dirs.Load(),
		Files:   c.files.Load(),
		Bytes:   c.bytes.Load(),
		Errors:  c.errors.Load(),
		Links:   c.links.Load(),
		Skipped: c.skipped.Load(),
		Elapsed: time.Since(started),
	}
	if cur := c.current.Load(); cur != nil {
		p.Current = *cur
	}
	return p
}

// reporter periodically publishes a ProgressEvent until stop is closed. The
// send never blocks the walk: a consumer that is behind simply misses ticks.
func reporter(ch chan<- Event, c *counters, started time.Time, interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			select {
			case ch <- ProgressEvent{c.snapshot(started)}:
			default:
			}
		}
	}
}
