// Package supervisor runs the login routine on a schedule and exposes enough
// state for a control interface to report on it.
//
// The original Guardian loop (internal/login.Guardian) never returns and gives
// callers no way to stop it, which is fine for a command line tool that runs
// until the machine reboots but useless for a service that has to be stopped or
// restarted on request.
package supervisor

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Mmx233/BitSrunLoginGo/internal/config"
	"github.com/Mmx233/BitSrunLoginGo/internal/config/keys"
	"github.com/Mmx233/BitSrunLoginGo/internal/login"
	log "github.com/sirupsen/logrus"
)

// State describes whether the loop is currently running.
type State string

const (
	StateStopped State = "stopped"
	StateRunning State = "running"
)

var (
	// ErrAlreadyRunning is returned by Start when the loop is already active.
	ErrAlreadyRunning = errors.New("supervisor: already running")

	// ErrStopTimeout is returned by Stop when the in-flight login did not
	// finish within the caller's deadline. The loop is marked stopped anyway.
	ErrStopTimeout = errors.New("supervisor: stop timed out")
)

// Status is a snapshot of the loop state, safe to serialise for a UI.
type Status struct {
	State       State     `json:"state"`
	StartedAt   time.Time `json:"started_at,omitzero"`
	LastRunAt   time.Time `json:"last_run_at,omitzero"`
	NextRunAt   time.Time `json:"next_run_at,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	RunCount    uint64    `json:"run_count"`
	FailCount   uint64    `json:"fail_count"`
	IntervalSec uint      `json:"interval_seconds"`
}

// Supervisor owns the login loop.
//
// The zero value is not usable; construct with New.
type Supervisor struct {
	logger log.FieldLogger

	mu       sync.Mutex
	state    State
	started  time.Time
	lastRun  time.Time
	nextRun  time.Time
	lastErr  string
	runs     uint64
	fails    uint64
	interval uint

	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a supervisor that has not been started yet.
func New(logger log.FieldLogger) *Supervisor {
	if logger == nil {
		logger = log.New()
	}
	return &Supervisor{
		logger: logger,
		state:  StateStopped,
	}
}

// Start begins the login loop. It starts the first attempt immediately and then
// repeats every Guardian.Duration seconds.
func (s *Supervisor) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == StateRunning {
		return ErrAlreadyRunning
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	s.state = StateRunning
	s.started = time.Now()

	go s.loop(ctx, s.done)
	return nil
}

func (s *Supervisor) loop(ctx context.Context, done chan struct{}) {
	defer close(done)

	for {
		s.runOnce(ctx)

		// The interval is read every iteration so that editing Guardian.Duration
		// takes effect on the next cycle instead of requiring a restart.
		interval := guardianDuration()
		s.mu.Lock()
		s.interval = uint(interval / time.Second)
		s.nextRun = time.Now().Add(interval)
		s.mu.Unlock()

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.mu.Lock()
			s.state = StateStopped
			s.nextRun = time.Time{}
			s.mu.Unlock()
			return
		case <-timer.C:
		}
	}
}

func (s *Supervisor) runOnce(ctx context.Context) {
	start := time.Now()

	err := login.Login(login.Conf{
		Logger:                      s.logger.WithField(keys.LogComponent, "login"),
		IsOnlineDetectLogDebugLevel: true,
	})

	s.mu.Lock()
	s.lastRun = start
	s.runs++
	if err != nil {
		s.fails++
		s.lastErr = err.Error()
	} else {
		s.lastErr = ""
	}
	s.mu.Unlock()

	if ctx.Err() != nil {
		return
	}
	s.logger.Debugln("login cycle finished in", time.Since(start))
}

// RunOnce performs a single login attempt without affecting the loop state.
// It is meant for the "login now" action of a control interface.
func (s *Supervisor) RunOnce(ctx context.Context) error {
	err := login.Login(login.Conf{
		Logger:                      s.logger.WithField(keys.LogComponent, "login"),
		IsOnlineDetectLogDebugLevel: true,
	})

	s.mu.Lock()
	s.lastRun = time.Now()
	s.runs++
	if err != nil {
		s.fails++
		s.lastErr = err.Error()
	} else {
		s.lastErr = ""
	}
	s.mu.Unlock()

	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return err
}

// Stop cancels the loop and waits for the in-flight login to return.
//
// A login in progress cannot be interrupted: it is a sequence of HTTP requests
// with its own timeouts. Stop therefore waits up to ctx's deadline and returns
// ErrStopTimeout if the cycle is still running. The supervisor is marked
// stopped either way so that a subsequent Start is not refused.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.state != StateRunning {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancel
	done := s.done
	s.state = StateStopped
	s.nextRun = time.Time{}
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ErrStopTimeout
	}
}

// Status returns a snapshot of the current loop state.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := Status{
		State:       s.state,
		LastRunAt:   s.lastRun,
		NextRunAt:   s.nextRun,
		LastError:   s.lastErr,
		RunCount:    s.runs,
		FailCount:   s.fails,
		IntervalSec: s.interval,
	}
	if s.state == StateRunning {
		st.StartedAt = s.started
	}
	return st
}

// guardianDuration returns the configured guardian interval, falling back to
// the historical default when Settings has not been loaded.
func guardianDuration() time.Duration {
	var seconds uint = 300
	if config.Settings != nil && config.Settings.Guardian.Duration > 0 {
		seconds = config.Settings.Guardian.Duration
	}
	return time.Duration(seconds) * time.Second
}
