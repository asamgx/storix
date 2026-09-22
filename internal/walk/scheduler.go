package walk

import "sync"

// task is one directory waiting to be listed.
type task struct {
	node   *Node
	path   string
	exempt bool // this directory lies under an exemption prefix
}

// scheduler hands directories to workers. It is a mutex-guarded LIFO stack
// with a condition variable and a pending counter, deliberately not a bounded
// channel: workers both produce and consume, so a bounded channel deadlocks as
// soon as every worker blocks on a send with no receiver left.
//
// pending counts queued plus in-flight directories. The walk is over when it
// reaches zero, which is what lets the last worker wake the ones parked in
// Wait. LIFO keeps the queue shallow (depth-first), which keeps memory low.
type scheduler struct {
	mu        sync.Mutex
	cond      *sync.Cond
	stack     []task
	pending   int
	cancelled bool
}

func newScheduler() *scheduler {
	s := &scheduler{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// push queues a directory. It never blocks.
func (s *scheduler) push(t task) {
	s.mu.Lock()
	s.pending++
	s.stack = append(s.stack, t)
	s.mu.Unlock()
	s.cond.Signal()
}

// pop returns the next directory, blocking while others are still in flight.
// It reports false when the walk is finished or cancelled.
func (s *scheduler) pop() (task, bool) {
	s.mu.Lock()
	for len(s.stack) == 0 && s.pending > 0 && !s.cancelled {
		s.cond.Wait()
	}
	if s.cancelled || len(s.stack) == 0 {
		s.mu.Unlock()
		return task{}, false
	}
	i := len(s.stack) - 1
	t := s.stack[i]
	s.stack[i] = task{} // drop the path reference
	s.stack = s.stack[:i]
	s.mu.Unlock()
	return t, true
}

// done marks a popped directory as finished.
func (s *scheduler) done() {
	s.mu.Lock()
	s.pending--
	last := s.pending == 0
	s.mu.Unlock()
	if last {
		s.cond.Broadcast()
	}
}

// cancel wakes every worker and stops the walk.
func (s *scheduler) cancel() {
	s.mu.Lock()
	s.cancelled = true
	s.mu.Unlock()
	s.cond.Broadcast()
}
