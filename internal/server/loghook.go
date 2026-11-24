package server

import (
	"strings"
	"time"

	"github.com/Mmx233/BitSrunLoginGo/internal/config"
	log "github.com/sirupsen/logrus"
)

// logHook copies logrus entries into the in-memory buffer that backs
// GET /api/logs.
//
// Registering a hook is preferable to reading the log file: the file path and
// name are user editable, may be empty, and the process also writes to
// stdout, so the file is not guaranteed to exist at all.
type logHook struct {
	buf    *logBuffer
	logger log.FieldLogger
}

func newLogHook(buf *logBuffer, logger log.FieldLogger) *logHook {
	return &logHook{buf: buf, logger: logger}
}

// Levels reports every level, including Panic, so the buffer also records the
// fatal line that precedes an exit.
func (h *logHook) Levels() []log.Level {
	return log.AllLevels
}

// Fire stores a formatted copy of the entry.
func (h *logHook) Fire(entry *log.Entry) error {
	msg := entry.Message
	// Prefix the component field the codebase uses to tag subsystems, so the
	// UI can show "login:" / "guard:" without a second column.
	if comp, ok := entry.Data["comp"].(string); ok && comp != "" {
		msg = comp + ": " + msg
	}

	t := entry.Time
	if t.IsZero() {
		t = time.Now()
	}
	h.buf.append(logEntry{
		Time:    t.Format("2006-01-02 15:04:05"),
		Level:   entry.Level.String(),
		Message: strings.TrimRight(msg, "\n"),
	})
	return nil
}

// attachLogHook registers the buffer on the logger the rest of the program
// writes through.
func (s *Server) attachLogHook() {
	if s.logs == nil {
		return
	}
	s.logs.mu.Lock()
	already := s.logs.hooked
	s.logs.mu.Unlock()
	if already {
		return
	}

	// Hooks live on the *logrus.Logger, but callers (main, the handlers) pass
	// a log.FieldLogger, which is usually a *logrus.Entry. Resolve the
	// underlying logger rather than assuming the concrete type, otherwise the
	// hook silently never fires and the log page stays empty.
	target := config.Logger
	if logger, ok := underlyingLogger(s.logger); ok {
		target = logger
	}
	if target != nil {
		target.AddHook(newLogHook(s.logs, s.logger))
	}

	s.logs.mu.Lock()
	s.logs.hooked = true
	s.logs.mu.Unlock()
}

// underlyingLogger digs the *logrus.Logger out of whatever logger was handed
// in. AddHook only exists on the logger, not on an entry.
func underlyingLogger(l log.FieldLogger) (*log.Logger, bool) {
	switch v := l.(type) {
	case *log.Logger:
		return v, true
	case *log.Entry:
		if v.Logger != nil {
			return v.Logger, true
		}
		return nil, false
	default:
		return nil, false
	}
}
