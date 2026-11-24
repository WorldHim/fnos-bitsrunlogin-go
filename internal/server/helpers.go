package server

import (
	"context"
	"net/http"
	"runtime"
	"time"
)

// goVersion reports the Go runtime the binary was built with.
func goVersion() string {
	return runtime.Version()
}

// contextWithTimeout derives a context from the request, so that a client
// hanging up cancels the work, with an upper bound of its own.
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

// newest returns the sequence number of the most recent buffered entry.
func (b *logBuffer) newest() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.next
}
