package desktop

import (
	"sync"
	"time"
)

// Lifecycle arbitrates asynchronous user confirmation against forced shutdown.
// A rejected or stale response never discards the running document. Cleanup and
// native exit happen once, independently of the native Run method returning.
type Lifecycle struct {
	mu       sync.Mutex
	state    string
	request  uint64
	timer    *time.Timer
	cleanup  func() error
	exit     func(error)
	confirm  func(uint64)
	once     sync.Once
	done     chan struct{}
	stopping chan struct{}
}

const (
	Running             = "running"
	Confirming          = "confirming"
	Stopping            = "stopping"
	Stopped             = "stopped"
	confirmationTimeout = 30 * time.Second
)

func NewLifecycle(cleanup func() error, exit func(error), confirm func(uint64)) *Lifecycle {
	return &Lifecycle{state: Running, cleanup: cleanup, exit: exit, confirm: confirm,
		done: make(chan struct{}), stopping: make(chan struct{})}
}

func (l *Lifecycle) State() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// RequestQuit returns immediately so native UI callbacks cannot deadlock on a
// dialog or on HTTP preference writes. Repeated close/quit events are coalesced.
func (l *Lifecycle) RequestQuit() {
	l.mu.Lock()
	if l.state != Running {
		l.mu.Unlock()
		return
	}
	l.state = Confirming
	l.request++
	id := l.request
	l.timer = time.AfterFunc(confirmationTimeout, func() { l.Respond(id, false) })
	l.mu.Unlock()
	l.confirm(id)
}

func (l *Lifecycle) Respond(id uint64, approved bool) bool {
	l.mu.Lock()
	if l.state != Confirming || id != l.request {
		l.mu.Unlock()
		return false
	}
	l.timer.Stop()
	l.state = Running
	if approved {
		l.state = Stopping
	}
	l.mu.Unlock()
	if approved {
		go l.Stop()
	}
	return true
}

// Stop is used for signals/backend failure and bypasses user confirmation.
// Controller.Shutdown supplies fresh bounded cleanup contexts.
func (l *Lifecycle) Stop() {
	l.once.Do(func() {
		l.mu.Lock()
		l.state = Stopping
		if l.timer != nil {
			l.timer.Stop()
		}
		l.mu.Unlock()
		close(l.stopping)
		err := l.cleanup()
		l.mu.Lock()
		l.state = Stopped
		l.mu.Unlock()
		close(l.done)
		l.exit(err)
	})
}

func (l *Lifecycle) Done() <-chan struct{} { return l.done }

func (l *Lifecycle) Stopping() <-chan struct{} { return l.stopping }
