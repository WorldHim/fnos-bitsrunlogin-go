package server

import (
	"encoding/json"
	"sync"
)

// defaultLogCapacity is how many entries the in-memory ring buffer keeps. The
// fnOS package redirects the process output to a file as well; this buffer
// exists so the UI can show recent activity without reading that file (whose
// path the user may change at any time).
const defaultLogCapacity = 512

// logEntry is one line kept for GET /api/logs.
type logEntry struct {
	Seq     uint64 `json:"seq"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// logBuffer is a fixed size ring buffer of log entries, safe for concurrent
// use. Writes never block the producer for long: the buffer only ever holds
// defaultLogCapacity entries.
type logBuffer struct {
	mu       sync.RWMutex
	capacity int
	next     uint64
	hooked   bool
	entries  []logEntry
}

func newLogBuffer(capacity int) *logBuffer {
	if capacity <= 0 {
		capacity = defaultLogCapacity
	}
	return &logBuffer{
		capacity: capacity,
		entries:  make([]logEntry, 0, capacity),
	}
}

// append adds an entry, dropping the oldest one when the buffer is full.
func (b *logBuffer) append(e logEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.next++
	e.Seq = b.next
	if len(b.entries) == b.capacity {
		copy(b.entries, b.entries[1:])
		b.entries[len(b.entries)-1] = e
		return
	}
	b.entries = append(b.entries, e)
}

// snapshot returns up to limit most recent entries and the sequence number of
// the newest one, so the UI can poll for what it has not seen yet.
func (b *logBuffer) snapshot(limit int) ([]logEntry, uint64) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	last := b.next
	if limit <= 0 || limit > len(b.entries) {
		limit = len(b.entries)
	}
	start := len(b.entries) - limit
	out := make([]logEntry, limit)
	copy(out, b.entries[start:])
	return out, last
}

// since returns entries with Seq greater than after, up to limit.
func (b *logBuffer) since(after uint64, limit int) []logEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([]logEntry, 0, len(b.entries))
	for _, e := range b.entries {
		if e.Seq <= after {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// jsonMarshal is a tiny indirection so the rest of the package does not import
// encoding/json in several files with different aliases.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
