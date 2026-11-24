// Package server exposes the login loop and its configuration over HTTP.
//
// It exists for the fnOS package, which needs a long running process it can
// stop and restart from the desktop, and a browser UI to edit the settings
// that would be unwieldy to collect in an installation wizard. The command
// line mode is unaffected: this package is only reached when --server is
// given.
//
// Routing follows the fnOS documentation: an app served on its own port is
// free to choose its paths, and the native sample uses "/api" for the backend.
// The routes are written without a prefix; when the package is published
// behind the unified gateway the prefix is stripped before matching, and the
// UI derives its base path from the URL it was loaded at.
package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mmx233/BitSrunLoginGo/internal/config"
	"github.com/Mmx233/BitSrunLoginGo/internal/config/keys"
	"github.com/Mmx233/BitSrunLoginGo/internal/supervisor"
	log "github.com/sirupsen/logrus"
)

// UISources holds the built web UI. The placeholder file keeps go:embed
// satisfiable before the front end has been built; `go build` fails on an
// empty embed pattern, so dropping the file would break the CLI build too.
//
//go:embed all:dist
var UISources embed.FS

// Options configure a server.
type Options struct {
	// Addr is the listen address, for example "127.0.0.1:9877". A value
	// starting with "unix:" names a Unix socket instead, which is what the
	// fnOS gateway forwards to.
	Addr string

	// GatewayPrefix is the path the fnOS gateway publishes this app under,
	// for example "/app/bitsrunlogin-go". Requests arrive with that prefix and
	// it is stripped before routing. Empty means no prefix.
	GatewayPrefix string

	// ConfigPath is the configuration file to read and write.
	ConfigPath string

	// Logger receives the server's own log entries.
	Logger log.FieldLogger

	// AutoStart starts the login loop as soon as the server is listening.
	// The fnOS package sets this so that opening the app does not require a
	// second click; a command line user probably does not want a background
	// login just from inspecting the status.
	AutoStart bool

	// Version is reported by GET /api/version.
	Version string
}

// Server is the HTTP control server.
type Server struct {
	opts      Options
	logger    log.FieldLogger
	store     *ConfigStore
	sup       *supervisor.Supervisor
	addr      string
	logs      *logBuffer
	handler   http.Handler
	startedAt time.Time

	mu     sync.Mutex
	server *http.Server
	lis    net.Listener
}

// New builds a server. Call ListenAndServe or Start to run it.
func New(opts Options) (*Server, error) {
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:9877"
	}
	if opts.ConfigPath == "" {
		return nil, errors.New("server: ConfigPath is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = config.Logger.WithField(keys.LogComponent, "server")
	}

	s := &Server{
		opts:      opts,
		logger:    logger,
		store:     NewConfigStore(opts.ConfigPath),
		sup:       supervisor.New(config.Logger.WithField(keys.LogComponent, "supervisor")),
		logs:      newLogBuffer(defaultLogCapacity),
		startedAt: time.Now(),
	}
	s.handler = s.routes()
	s.attachLogHook()
	return s, nil
}

// routes wires the mux. Kept separate so tests can build a handler without
// opening a socket.
// routes() builds the routing table.
//
// When the server is reached through the fnOS gateway the request path still
// carries the gateway prefix: the gateway forwards /app/bitsrunlogin-go/api/
// status unchanged rather than stripping it. The prefix is removed once here so
// every handler and the static file server keep seeing the paths they were
// written for, and the same binary also works when the prefix is absent (direct
// access during development).
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/login", s.handleLoginNow)

	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)

	mux.HandleFunc("GET /api/service", s.handleServiceState)
	mux.HandleFunc("POST /api/service/start", s.handleServiceStart)
	mux.HandleFunc("POST /api/service/stop", s.handleServiceStop)
	mux.HandleFunc("POST /api/service/restart", s.handleServiceRestart)

	mux.HandleFunc("GET /api/logs", s.handleLogs)

	mux.Handle("/", s.uiHandler())

	return s.withMiddleware(stripGatewayPrefix(mux, s.gatewayPrefix()))
}

// stripGatewayPrefix removes the fnOS gateway prefix from the request path
// before dispatching, so the routing table stays written against "/api/...".
//
// Doing it here rather than with http.StripPrefix avoids a corner case: a
// request for the bare prefix would be rewritten to an empty path, which is not
// a legal request target. Everything already without the prefix passes through
// untouched, so the same handler also serves direct access.
func stripGatewayPrefix(next http.Handler, prefix string) http.Handler {
	if prefix == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			// A bare mount point has no trailing slash, so the browser
			// would resolve every relative URL against the parent
			// directory. Send it to the directory form first.
			if path == prefix && r.Method == http.MethodGet {
				redirectPath := path + "/"
				if r.URL.RawQuery != "" {
					redirectPath += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, redirectPath, http.StatusMovedPermanently)
				return
			}
			trimmed := strings.TrimPrefix(path, prefix)
			if trimmed == "" {
				trimmed = "/"
			}
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = trimmed
			r2.URL.RawPath = ""
			// The UI needs the path the browser used to build its base
			// href; after this point only the trimmed path is visible.
			r2 = r2.WithContext(context.WithValue(r.Context(), originalPathKey{}, path))
			next.ServeHTTP(w, r2)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// gatewayPrefix returns the path the fnOS gateway publishes this app under,
// with any trailing slash removed. An empty result means the server is reached
// directly and paths need no rewriting.
func (s *Server) gatewayPrefix() string {
	return strings.TrimSuffix(s.opts.GatewayPrefix, "/")
}

// uiHandler serves the embedded front end with a SPA fallback.
func (s *Server) uiHandler() http.Handler {
	sub, err := fs.Sub(UISources, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "embedded UI unavailable", http.StatusInternalServerError)
		})
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Unknown /api paths must stay a JSON 404. Falling back to the SPA
		// shell would answer 200 with HTML and hide front end routing bugs
		// behind a parse error.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "unknown API endpoint")
			return
		}

		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean != "" {
			if _, statErr := fs.Stat(sub, clean); statErr == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		serveIndex(w, r, sub)
	})
}

// originalPathKey carries the path the client requested through the context,
// so a handler that only sees the trimmed path can still tell where the page
// was mounted.
type originalPathKey struct{}

// originalPath returns the path the request arrived with.
func originalPath(r *http.Request) string {
	if p, ok := r.Context().Value(originalPathKey{}).(string); ok {
		return p
	}
	return r.URL.Path
}

// indexBootScript points the document's asset and API base at the directory
// the page was served from.
//
// The UI is published under a gateway prefix and can be opened at a deep link
// such as /app/myapp/logs. Relative asset URLs then resolve against the last
// segment ("/app/myapp/"), not the mount point, so app.css would be requested
// from /app/ and 404. A relative base has the same problem. Resolving against
// the path the request actually arrived at, without its last segment and with
// a trailing slash, is correct for a bare mount point, a deep link and a
// direct loopback start alike. It is emitted rather than read from request
// context because the handler stays an ordinary http.Handler.
func indexBootScript(r *http.Request) []byte {
	base := path.Dir(originalPath(r))
	if base != "/" {
		base += "/"
	}
	quoted, err := json.Marshal(base)
	if err != nil {
		return nil
	}
	return []byte("<base href=" + string(quoted) + ">\n" +
		"<script>window.__BS_BASE__ = " + string(quoted) + ";</script>\n")
}

// serveIndex answers with the SPA shell, whose <head> receives the base tag
// for the current request path.
func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	index, readErr := fs.ReadFile(sub, "index.html")
	if readErr != nil {
		http.Error(w, "UI not built", http.StatusNotFound)
		return
	}
	// The tag goes right after <head> so it applies to every asset the shell
	// references, including ones added later by the page.
	injected := index
	if script := indexBootScript(r); script != nil {
		if at := bytes.Index(index, []byte("<head>")); at >= 0 {
			at += len("<head>")
			injected = make([]byte, 0, len(index)+len(script))
			injected = append(injected, index[:at]...)
			injected = append(injected, '\n')
			injected = append(injected, script...)
			injected = append(injected, index[at:]...)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(injected)
}

// withMiddleware adds request logging, panic recovery and the CSRF guard.
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Errorf("panic serving %s %s: %v", r.Method, r.URL.Path, rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()

		if r.Method != http.MethodGet && r.Method != http.MethodHead && !s.allowWrite(r) {
			writeError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Debugf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}

// statusRecorder captures the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// sameOrigin rejects writes whose Origin header names another host.
//
// The server binds to loopback, but a page open in the same browser can still
// reach 127.0.0.1, so a form on any website would otherwise be able to change
// the configuration. Requests without an Origin header (curl, the desktop
// shell) are allowed.
// allowWrite reports whether a request that changes state may proceed.
//
// Behind the fnOS gateway the browser cannot reach this process directly: the
// request only arrives after the gateway has checked the user's session, and
// it may rewrite Host along the way, so comparing Origin with Host would
// reject perfectly good writes. The gateway is the CSRF boundary there.
//
// Anywhere else - a directly reachable port - nothing else vouches for the
// caller, so the Origin check stays.
func (s *Server) allowWrite(r *http.Request) bool {
	if s.gatewayPrefix() != "" {
		return true
	}
	return sameOrigin(r)
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if origin == "null" {
		return false
	}
	host := origin
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host = strings.TrimSuffix(host, "/")
	return strings.EqualFold(host, r.Host)
}

// ListenAndServe binds the configured address and serves until ctx is
// cancelled.
//
// An address of the form "unix:/path/to/app.sock" creates a Unix socket
// instead of a TCP listener. fnOS routes requests to such a socket through its
// unified gateway, which is the only way a desktop iframe can reach a service
// on another machine: a loopback TCP port only exists on the NAS itself, so the
// browser that renders the iframe would connect to its own machine.
//
// It returns nil on a clean shutdown, so callers do not have to special case
// http.ErrServerClosed.
func (s *Server) ListenAndServe(ctx context.Context) error {
	lis, err := listen(s.opts.Addr)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.lis = lis
	s.addr = lis.Addr().String()
	s.server = &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	srv := s.server
	s.mu.Unlock()

	s.logger.Infoln("control server listening on http://" + s.addr)

	// The login loop is started before serving so that the UI never sees a
	// stopped loop that the operator did not stop.
	//
	// A fresh install has no account yet. Logging in with it would hit the
	// campus portal with an empty username and surface a JSON parse error from
	// whatever the portal answers, so stay idle and let the UI ask for the
	// settings instead.
	if s.opts.AutoStart {
		if err := s.checkConfigured(); err != nil {
			s.logger.Infoln("login not started:", err, "- open the app and fill in the account settings")
		} else if !config.Settings.Guardian.Enable {
			// With guardian disabled the CLI performs a single login; mirror that
			// instead of looping behind the user's back.
			go func() {
				if err := s.sup.RunOnce(ctx); err != nil {
					s.logger.Warnln("initial login failed:", err)
				}
			}()
		} else if err := s.sup.Start(); err != nil && !errors.Is(err, supervisor.ErrAlreadyRunning) {
			s.logger.Warnln("start login loop:", err)
		}
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(lis)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = s.sup.Stop(stopCtx)
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	}
}

// Addr returns the bound address, useful when the caller passed port 0.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// listen opens the listener described by addr. A "unix:" prefix selects a Unix
// socket; anything else is treated as a TCP address.
//
// A socket file left behind by a previous run would make the bind fail with
// "address already in use" even though nothing is listening, so a stale one is
// removed first. Only a socket that nothing answers on is removed: probing with
// a dial keeps a live process from losing its listening socket.
func listen(addr string) (net.Listener, error) {
	path, isUnix := strings.CutPrefix(addr, "unix:")
	if !isUnix {
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("listen %s: %w", addr, err)
		}
		return lis, nil
	}

	if path == "" {
		return nil, errors.New("server: unix address has no path")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("prepare socket directory: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, time.Second)
		if dialErr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("listen %s: another process is already using it", addr)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	}

	lis, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	// The gateway runs as root and forwards to this process; the package user
	// owns the socket, so it has to be reachable for both.
	if err := os.Chmod(path, 0o660); err != nil {
		_ = lis.Close()
		return nil, fmt.Errorf("set socket permissions: %w", err)
	}
	return lis, nil
}

// Handler exposes the routing table for tests.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// --- helpers --------------------------------------------------------------

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	data, err := jsonMarshal(v)
	if err != nil {
		return
	}
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// requireJSON enforces a JSON content type on writes, so that a cross-site
// HTML form cannot reach the handlers even without CORS preflight.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" || !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}

// parseUintQuery reads an optional unsigned integer query parameter.
func parseUintQuery(r *http.Request, name string, def uint) (uint, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return def, fmt.Errorf("invalid %s: %w", name, err)
	}
	return uint(v), nil
}

// envOr returns the environment variable or a fallback.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
