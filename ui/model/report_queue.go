package model

import "sync"

// reportQueue runs the playback reports to providers one at a time, in the
// order that Update added them. So the now-playing, progress and scrobble
// reports of a track reach the provider in order, as the favorite calls do
// through favorites.SyncQueue. Unlike that queue, it skips no report. It runs
// the reports on one goroutine that it starts when a report waits and that
// ends when none waits, so add never blocks Update.
type reportQueue struct {
	mu      sync.Mutex
	pending []func()
	running bool
}

// add queues report and returns at once.
func (q *reportQueue) add(report func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, report)
	if !q.running {
		q.running = true
		go q.drain()
	}
}

// drain runs the queued reports until none waits.
func (q *reportQueue) drain() {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		report := q.pending[0]
		q.pending[0] = nil
		q.pending = q.pending[1:]
		q.mu.Unlock()
		report()
	}
}
