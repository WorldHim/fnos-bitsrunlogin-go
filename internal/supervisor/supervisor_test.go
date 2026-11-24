package supervisor_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mmx233/BitSrunLoginGo/internal/supervisor"
	log "github.com/sirupsen/logrus"
)

func quietLogger() *log.Logger {
	l := log.New()
	l.SetLevel(log.PanicLevel)
	return l
}

func TestNewStartsStopped(t *testing.T) {
	s := supervisor.New(quietLogger())

	st := s.Status()
	if st.State != supervisor.StateStopped {
		t.Errorf("initial state = %q, want %q", st.State, supervisor.StateStopped)
	}
	if st.RunCount != 0 || st.FailCount != 0 {
		t.Errorf("initial counters are not zero: %+v", st)
	}
	if !st.StartedAt.IsZero() {
		t.Error("StartedAt must be zero before Start")
	}
}

func TestStopWithoutStartIsNoop(t *testing.T) {
	s := supervisor.New(quietLogger())

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop on a stopped supervisor returned %v, want nil", err)
	}
	if st := s.Status(); st.State != supervisor.StateStopped {
		t.Errorf("state = %q, want stopped", st.State)
	}
}

func TestNewNilLoggerFallsBack(t *testing.T) {
	// Must not panic when the caller passes nil.
	s := supervisor.New(nil)
	if s == nil {
		t.Fatal("New(nil) returned nil")
	}
	if st := s.Status(); st.State != supervisor.StateStopped {
		t.Errorf("state = %q, want stopped", st.State)
	}
}

func TestStartTwiceReturnsErrAlreadyRunning(t *testing.T) {
	s := supervisor.New(quietLogger())

	if err := s.Start(); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	}()

	if err := s.Start(); !errors.Is(err, supervisor.ErrAlreadyRunning) {
		t.Fatalf("second Start = %v, want ErrAlreadyRunning", err)
	}
}

func TestStatusReportsRunningAndStartedAt(t *testing.T) {
	s := supervisor.New(quietLogger())

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	}()

	st := s.Status()
	if st.State != supervisor.StateRunning {
		t.Errorf("state = %q, want running", st.State)
	}
	if st.StartedAt.IsZero() {
		t.Error("StartedAt is zero while running")
	}
}

// Stop must leave the supervisor in a state that accepts Start again.
func TestStopAllowsRestart(t *testing.T) {
	s := supervisor.New(quietLogger())

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	st := s.Status()
	if st.State != supervisor.StateStopped {
		t.Errorf("state after Stop = %q, want stopped", st.State)
	}
	if !st.NextRunAt.IsZero() {
		t.Error("NextRunAt must be cleared after Stop")
	}

	if err := s.Start(); err != nil {
		t.Fatalf("restart after Stop failed: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	_ = s.Stop(ctx2)
}

// Status must be safe to call while the loop is running. Under -race this is
// the test that catches unsynchronised access to the counters.
func TestStatusIsConcurrencySafe(t *testing.T) {
	s := supervisor.New(quietLogger())

	if err := s.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadline := time.Now().Add(150 * time.Millisecond)
			for time.Now().Before(deadline) {
				_ = s.Status()
			}
		}()
	}
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

// A stopped supervisor must accept RunOnce; RunOnce must not change the loop
// state, only the counters.
func TestRunOnceDoesNotChangeState(t *testing.T) {
	s := supervisor.New(quietLogger())

	before := s.Status()
	if before.State != supervisor.StateStopped {
		t.Fatalf("precondition: state = %q", before.State)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = s.RunOnce(ctx)

	after := s.Status()
	if after.State != supervisor.StateStopped {
		t.Errorf("RunOnce changed state to %q, want stopped", after.State)
	}
	if after.RunCount != before.RunCount+1 {
		t.Errorf("RunCount = %d, want %d", after.RunCount, before.RunCount+1)
	}
}

func TestRunOnceReportsCancellation(t *testing.T) {
	s := supervisor.New(quietLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce with a cancelled context = %v, want context.Canceled", err)
	}
}
