package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
)

// quietLogger returns a logger that discards output so test runs stay readable.
func quietLogger() *log.Logger {
	l := log.New()
	l.SetLevel(log.PanicLevel)
	return l
}

func newTestServer(t *testing.T, content string) (*Server, string) {
	t.Helper()
	return newTestServerOpts(t, content, Options{})
}

func newTestServerOpts(t *testing.T, content string, opts Options) (*Server, string) {
	t.Helper()
	if content == "" {
		content = testConfig
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "Config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	opts.Addr = "127.0.0.1:0"
	opts.ConfigPath = path
	opts.Logger = quietLogger()
	opts.Version = "test"

	srv, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, path
}

func doRequest(t *testing.T, h http.Handler, method, target string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestVersionEndpoint(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodGet, "/api/version", "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp versionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version != "test" {
		t.Errorf("version = %q, want test", resp.Version)
	}
	if resp.GoVersion == "" {
		t.Error("go_version must not be empty")
	}
}

func TestStatusEndpoint(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodGet, "/api/status", "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp statusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Supervisor.State == "" {
		t.Error("supervisor state must be reported")
	}
	if resp.Form.Username == "" {
		t.Error("form summary is missing")
	}
	if strings.Contains(w.Body.String(), "s3cr3t-value") {
		t.Error("status endpoint must not leak the password")
	}
}

func TestGetConfigEndpoint(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodGet, "/api/config", "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), "s3cr3t-value") {
		t.Error("config endpoint must not leak the password")
	}
	var view ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !view.Form.HasPassword {
		t.Error("has_password should be true")
	}
}

func TestPutConfigRequiresJSONContentType(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodPut, "/api/config", `{}`, map[string]string{
		"Content-Type": "text/plain",
	})
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", w.Code)
	}
}

func TestPutConfigRejectsCrossOrigin(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodPut, "/api/config",
		`{"form":{"username":"evil"}}`,
		map[string]string{
			"Content-Type": "application/json",
			"Origin":       "http://evil.example",
		})
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestPutConfigRejectsUnknownFields(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodPut, "/api/config",
		`{"form":{"username":"bob"},"bogus":1}`,
		map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPutConfigUpdatesAndPreservesSecrets(t *testing.T) {
	srv, path := newTestServer(t, "")

	w := doRequest(t, srv.Handler(), http.MethodPut, "/api/config",
		`{"form":{"username":"bob"}}`,
		map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "bob") {
		t.Errorf("saved config does not contain the new username: %s", data)
	}
	if !strings.Contains(string(data), "s3cr3t-value") {
		t.Error("save dropped the password that the patch did not mention")
	}
}

func TestPutConfigRejectsInvalidValues(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodPut, "/api/config",
		`{"form":{"domain":""}}`,
		map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestServiceEndpoints(t *testing.T) {
	srv, _ := newTestServer(t, "")

	w := doRequest(t, srv.Handler(), http.MethodGet, "/api/service", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var svc serviceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &svc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if svc.State != "stopped" {
		t.Errorf("initial state = %q, want stopped", svc.State)
	}

	w = doRequest(t, srv.Handler(), http.MethodPost, "/api/service/start", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200: %s", w.Code, w.Body.String())
	}

	// Starting twice is not an error: the UI may retry.
	w = doRequest(t, srv.Handler(), http.MethodPost, "/api/service/start", "", nil)
	if w.Code != http.StatusOK {
		t.Errorf("second start status = %d, want 200", w.Code)
	}

	w = doRequest(t, srv.Handler(), http.MethodPost, "/api/service/stop", "", nil)
	if w.Code != http.StatusOK && w.Code != http.StatusAccepted {
		t.Errorf("stop status = %d, want 200 or 202", w.Code)
	}
}

func TestLogsEndpoint(t *testing.T) {
	srv, _ := newTestServer(t, "")
	srv.logs.append(logEntry{Level: "info", Message: "hello"})

	w := doRequest(t, srv.Handler(), http.MethodGet, "/api/logs?limit=10", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp logsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].Message != "hello" {
		t.Errorf("entries = %+v, want one entry saying hello", resp.Entries)
	}
	if resp.LastSeq == 0 {
		t.Error("last_seq must be reported for paging")
	}

	w = doRequest(t, srv.Handler(), http.MethodGet, "/api/logs?after="+itoa(resp.LastSeq), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("paged status = %d, want 200", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("expected no new entries, got %+v", resp.Entries)
	}
}

func TestUnknownAPIRouteDoesNotServeIndex(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodGet, "/api/nope", "", nil)

	// The SPA fallback would answer 200 with index.html, which hides routing
	// bugs from the front end; /api must stay JSON-only.
	if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "<html") {
		t.Error("/api/* must not fall back to the SPA shell")
	}
}

func TestUIRootServesIndex(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodGet, "/", "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content type = %q, want text/html", w.Header().Get("Content-Type"))
	}
}

func TestSPAFallbackServesIndexForDeepLink(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodGet, "/settings/advanced", "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<html") {
		t.Errorf("expected the shell, got %s", w.Body.String())
	}
}

// itoa avoids importing strconv in the test for one call.
func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func TestLogHookCapturesFromFieldLogger(t *testing.T) {
	// Regression test: the server is given a *logrus.Entry, and an
	// implementation that only type-asserts *logrus.Logger silently never
	// fires, leaving the log page permanently empty.
	logger := log.New()
	logger.SetLevel(log.InfoLevel)
	logger.SetOutput(io.Discard)

	srv, err := New(Options{
		Addr:       "127.0.0.1:0",
		ConfigPath: filepath.Join(t.TempDir(), "Config.yaml"),
		Logger:     logger.WithField("comp", "server"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	logger.Info("hello from the hook")

	entries, _ := srv.logs.snapshot(10)
	if len(entries) == 0 {
		t.Fatal("the hook did not capture the entry")
	}
	if !strings.Contains(entries[0].Message, "hello from the hook") {
		t.Errorf("message = %q, want it to contain the log line", entries[0].Message)
	}

	// The component prefix comes from the "comp" field, which only entries
	// derived from WithField carry. This is how the program logs.
	fieldLogger := logger.WithField("comp", "login")
	fieldLogger.Warn("through a field logger")

	entries, _ = srv.logs.snapshot(10)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Message, "login:") {
			found = true
		}
	}
	if !found {
		t.Errorf("entries = %+v, want one prefixed with the comp field", entries)
	}
}

// The fnOS gateway forwards requests with its prefix still attached. Routing
// has to keep working for both the prefixed and the bare path, because the same
// binary also runs directly during development.
func TestGatewayPrefixIsStripped(t *testing.T) {
	srv, _ := newTestServerOpts(t, "", Options{GatewayPrefix: "/app/bitsrunlogin-go"})

	for _, target := range []string{
		"/app/bitsrunlogin-go/api/version",
		"/api/version",
	} {
		w := doRequest(t, srv.Handler(), "GET", target, "", nil)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200 (%s)", target, w.Code, w.Body.String())
			continue
		}
		var got versionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Errorf("GET %s decode: %v", target, err)
			continue
		}
		if got.Version != "test" {
			t.Errorf("GET %s version = %q, want test", target, got.Version)
		}
	}
}

func TestGatewayPrefixServesUI(t *testing.T) {
	srv, _ := newTestServerOpts(t, "", Options{GatewayPrefix: "/app/bitsrunlogin-go"})

	w := doRequest(t, srv.Handler(), "GET", "/app/bitsrunlogin-go/", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /app/bitsrunlogin-go/ status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// The shell must tell the browser where its assets live. Without this the
// page loaded at a deep link requests app.css relative to the last path
// segment, which is what produced the 404s in the fnOS desktop.
func TestGatewayPrefixInjectsBasePath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"mount point", "/app/bitsrunlogin-go/", `<base href="/app/bitsrunlogin-go/">`},
		{"deep link", "/app/bitsrunlogin-go/logs", `<base href="/app/bitsrunlogin-go/">`},
		{"root", "/", `<base href="/">`},
	}

	srv, _ := newTestServerOpts(t, "", Options{GatewayPrefix: "/app/bitsrunlogin-go"})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, srv.Handler(), "GET", tc.path, "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200", tc.path, w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("GET %s body does not contain %s\nbody:\n%s", tc.path, tc.want, body)
			}
			if !strings.Contains(body, `window.__BS_BASE__`) {
				t.Errorf("GET %s body does not carry the JS base path", tc.path)
			}
		})
	}
}

// A bare mount point must land on the directory form, otherwise the browser
// resolves relative URLs against the parent directory.
func TestGatewayPrefixRedirectsBareMountPoint(t *testing.T) {
	srv, _ := newTestServerOpts(t, "", Options{GatewayPrefix: "/app/bitsrunlogin-go"})

	w := doRequest(t, srv.Handler(), "GET", "/app/bitsrunlogin-go", "", nil)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/app/bitsrunlogin-go/" {
		t.Errorf("Location = %q, want /app/bitsrunlogin-go/", got)
	}
}

// The browser has to be able to fetch an asset the base tag points at.
func TestGatewayPrefixResolvesAssetsUnderDeepLinkBase(t *testing.T) {
	srv, _ := newTestServerOpts(t, "", Options{GatewayPrefix: "/app/bitsrunlogin-go"})

	w := doRequest(t, srv.Handler(), "GET", "/app/bitsrunlogin-go/app.css", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /app/bitsrunlogin-go/app.css status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Errorf("Content-Type = %q, want a css type", ct)
	}
}

// An unknown API path under the prefix must still answer with the JSON 404
// rather than the SPA shell.
func TestGatewayPrefixKeepsAPI404(t *testing.T) {
	srv, _ := newTestServerOpts(t, "", Options{GatewayPrefix: "/app/bitsrunlogin-go"})

	w := doRequest(t, srv.Handler(), "GET", "/app/bitsrunlogin-go/api/nope", "", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", w.Header().Get("Content-Type"))
	}
}

func TestStatusReportsConfigured(t *testing.T) {
	srv, _ := newTestServer(t, "")
	w := doRequest(t, srv.Handler(), http.MethodGet, "/api/status", "", nil)

	var resp statusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Configured {
		t.Error("configured = false, want true for a complete config")
	}
	if resp.MissingField != "" {
		t.Errorf("missing_field = %q, want empty", resp.MissingField)
	}
}

func TestStatusReportsMissingAccount(t *testing.T) {
	// A fresh install: the defaults ship without an account.
	fresh := `form:
  domain: ""
  username: ""
  user_type: cmcc
  password: ""
settings:
  basic:
    timeout: 5
  guardian:
    enable: true
    duration: 300
`
	srv, _ := newTestServer(t, fresh)
	w := doRequest(t, srv.Handler(), http.MethodGet, "/api/status", "", nil)

	var resp statusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Configured {
		t.Error("configured = true, want false before the account is filled in")
	}
	if resp.MissingField != "form.domain" {
		t.Errorf("missing_field = %q, want form.domain", resp.MissingField)
	}
	if resp.Supervisor.State == "running" {
		t.Error("the login loop must not run before the account is configured")
	}
}
