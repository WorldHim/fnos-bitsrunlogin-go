package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mmx233/BitSrunLoginGo/internal/config"
	"github.com/Mmx233/BitSrunLoginGo/pkg/srun"
)

func TestLogBufferDropsOldestWhenFull(t *testing.T) {
	b := newLogBuffer(3)
	for i := 1; i <= 5; i++ {
		b.append(logEntry{Message: "e"})
	}

	entries, last := b.snapshot(10)
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if last != 5 {
		t.Errorf("last seq = %d, want 5", last)
	}
	// Sequence numbers must stay increasing so the UI can page with `after`.
	if entries[0].Seq != 3 || entries[2].Seq != 5 {
		t.Errorf("seq range = %d..%d, want 3..5", entries[0].Seq, entries[2].Seq)
	}
}

func TestLogBufferSince(t *testing.T) {
	b := newLogBuffer(10)
	for i := 0; i < 4; i++ {
		b.append(logEntry{Message: "e"})
	}

	got := b.since(2, 0)
	if len(got) != 2 || got[0].Seq != 3 || got[1].Seq != 4 {
		t.Fatalf("since(2) = %+v, want entries 3 and 4", got)
	}

	if got := b.since(4, 0); len(got) != 0 {
		t.Errorf("since(newest) = %+v, want empty", got)
	}
}

func TestLogBufferSnapshotLimit(t *testing.T) {
	b := newLogBuffer(10)
	for i := 0; i < 5; i++ {
		b.append(logEntry{Message: "e"})
	}

	entries, _ := b.snapshot(2)
	if len(entries) != 2 || entries[0].Seq != 4 {
		t.Fatalf("snapshot(2) = %+v, want the two newest entries", entries)
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"no origin header", "", "127.0.0.1:9877", true},
		{"same origin", "http://127.0.0.1:9877", "127.0.0.1:9877", true},
		{"same origin other port", "http://127.0.0.1:8080", "127.0.0.1:9877", false},
		{"foreign host", "http://evil.example", "127.0.0.1:9877", false},
		{"null origin", "null", "127.0.0.1:9877", false},
		{"case insensitive host", "http://127.0.0.1:9877", "127.0.0.1:9877", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/api/service/start", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := sameOrigin(r); got != tc.want {
				t.Errorf("sameOrigin() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseUintQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/logs?limit=42", nil)
	v, err := parseUintQuery(r, "limit", 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 42 {
		t.Errorf("limit = %d, want 42", v)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	v, err = parseUintQuery(r, "limit", 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 200 {
		t.Errorf("default limit = %d, want 200", v)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/logs?limit=abc", nil)
	if _, err = parseUintQuery(r, "limit", 200); err == nil {
		t.Error("expected an error for a non numeric limit")
	}
}

// writeTestConfig writes a minimal configuration file and returns its path.
func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const testConfig = `form:
  domain: www.msftconnecttest.com
  username: alice
  user_type: cmcc
  password: s3cr3t-value
meta:
  n: "200"
  type: "1"
  acid: "5"
  enc: srun_bx1
  os: Windows 10
  info_prefix: SRBX1
settings:
  basic:
    timeout: 5
  guardian:
    enable: true
    duration: 300
  log:
    log_path: /tmp
  ddns:
    enable: true
    ttl: 600
    domain: home.example.com
    provider: cloudflare
    config:
      zone: abc123
      token: cf-token-value
`

func TestConfigViewHidesSecrets(t *testing.T) {
	store := NewConfigStore(writeTestConfig(t, testConfig))
	conf, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	view := NewConfigView(conf)
	if view.Form.HasPassword != true {
		t.Error("HasPassword should be true")
	}
	if view.Form.Username != "alice" {
		t.Errorf("Username = %q, want alice", view.Form.Username)
	}
	if !view.Settings.DDNS.Config.HasCloudflareToken {
		t.Error("HasCloudflareToken should be true")
	}

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Use distinctive secrets: the view legitimately contains the strings
	// "has_password" and "has_cloudflare_token", so matching on the field name
	// would give a false positive.
	for _, secret := range []string{"s3cr3t-value", "cf-token-value"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("serialised view leaks %q: %s", secret, encoded)
		}
	}
}

func TestConfigStoreSaveIsAtomicAndKeepsMode(t *testing.T) {
	path := writeTestConfig(t, testConfig)
	store := NewConfigStore(path)

	conf, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	conf.Form.Username = "bob"
	if err := store.Save(conf); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temporary file must not survive a successful save")
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Form.Username != "bob" {
		t.Errorf("username = %q, want bob", reloaded.Form.Username)
	}
	if reloaded.Form.Password != "s3cr3t-value" {
		t.Error("save must not lose untouched fields")
	}
}

func TestConfigStoreSaveRejectsEmptyPassword(t *testing.T) {
	path := writeTestConfig(t, testConfig)
	store := NewConfigStore(path)

	conf, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	conf.Form.Password = ""
	err = store.Save(conf)
	if err == nil {
		t.Fatal("expected validation to reject an empty password")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Errorf("error = %v, want it to mention password", err)
	}
}

func TestPatchPreservesSecretsWhenEmpty(t *testing.T) {
	conf := &config.ConfFromFile{}
	conf.Form.Password = "original"
	conf.Settings.DDNS.Config.Cloudflare.Token = "token"

	username := "carol"
	patch := ConfigPatch{Form: &FormPatch{Username: &username}}
	patch.Apply(conf)

	if conf.Form.Username != "carol" {
		t.Errorf("username = %q, want carol", conf.Form.Username)
	}
	if conf.Form.Password != "original" {
		t.Errorf("password = %q, want it preserved", conf.Form.Password)
	}
	if conf.Settings.DDNS.Config.Cloudflare.Token != "token" {
		t.Errorf("token = %q, want it preserved", conf.Settings.DDNS.Config.Cloudflare.Token)
	}
}

func TestPatchClearsSecretsWithFlag(t *testing.T) {
	conf := &config.ConfFromFile{}
	conf.Form.Password = "original"

	patch := ConfigPatch{Form: &FormPatch{ClearPassword: true}}
	patch.Apply(conf)

	if conf.Form.Password != "" {
		t.Errorf("password = %q, want cleared", conf.Form.Password)
	}
}

func TestPatchReplacesSecretsWhenProvided(t *testing.T) {
	conf := &config.ConfFromFile{}
	conf.Form.Password = "original"

	patch := ConfigPatch{Form: &FormPatch{Password: "newpass"}}
	patch.Apply(conf)

	if conf.Form.Password != "newpass" {
		t.Errorf("password = %q, want newpass", conf.Form.Password)
	}
}

func TestPatchKeepsOSNameAsGiven(t *testing.T) {
	conf := &config.ConfFromFile{}

	meta := srun.LoginMeta{OS: "Windows 10", Name: "windows"}
	patch := ConfigPatch{Meta: &meta}
	patch.Apply(conf)

	if conf.Meta.OS != "Windows 10" || conf.Meta.Name != "windows" {
		t.Errorf("meta = %q/%q, want Windows 10/windows", conf.Meta.OS, conf.Meta.Name)
	}
}

func TestPatchDropsNameWhenOSIsEmpty(t *testing.T) {
	conf := &config.ConfFromFile{}
	conf.Meta.OS = "Windows 10"
	conf.Meta.Name = "windows"

	// A client that clears os but keeps the old name would otherwise leave
	// the two fields describing different things.
	meta := srun.LoginMeta{OS: "", Name: "windows"}
	patch := ConfigPatch{Meta: &meta}
	patch.Apply(conf)

	if conf.Meta.OS != "" {
		t.Errorf("os = %q, want empty", conf.Meta.OS)
	}
	if conf.Meta.Name != "" {
		t.Errorf("name = %q, want cleared with os", conf.Meta.Name)
	}
}

func TestJSONAndYAMLAreBothSupported(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "Config.json")
	if err := os.WriteFile(jsonPath, []byte(`{"form":{"domain":"d","username":"u","password":"p"}}`), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	store := NewConfigStore(jsonPath)
	conf, err := store.Load()
	if err != nil {
		t.Fatalf("load json: %v", err)
	}
	if conf.Form.Username != "u" {
		t.Errorf("username = %q, want u", conf.Form.Username)
	}

	conf.Form.Username = "v"
	if err := store.Save(conf); err != nil {
		t.Fatalf("save json: %v", err)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), `"username"`) {
		t.Errorf("saved file does not look like JSON: %s", data)
	}
}

func TestCheckConfiguredAcceptsCompleteConfig(t *testing.T) {
	conf := &config.ConfFromFile{
		Form: srun.LoginForm{
			Domain:   "portal.example.com",
			Username: "alice",
			Password: "secret",
		},
	}
	if err := CheckConfigured(conf); err != nil {
		t.Fatalf("CheckConfigured: %v", err)
	}
}

func TestCheckConfiguredReportsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		form srun.LoginForm
		want string
	}{
		{"empty", srun.LoginForm{}, "form.domain"},
		{"no domain", srun.LoginForm{Username: "a", Password: "b"}, "form.domain"},
		{"blank domain", srun.LoginForm{Domain: "  ", Username: "a", Password: "b"}, "form.domain"},
		{"no username", srun.LoginForm{Domain: "d", Password: "b"}, "form.username"},
		{"no password", srun.LoginForm{Domain: "d", Username: "a"}, "form.password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckConfigured(&config.ConfFromFile{Form: tc.form})
			if err == nil {
				t.Fatalf("CheckConfigured: want %s error, got nil", tc.want)
			}
			var missing *MissingFieldError
			if !errors.As(err, &missing) {
				t.Fatalf("CheckConfigured: error %v is not a MissingFieldError", err)
			}
			if missing.Field != tc.want {
				t.Errorf("missing field = %q, want %q", missing.Field, tc.want)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Error("a missing field must satisfy errors.Is(err, ErrInvalidConfig)")
			}
		})
	}
}

func TestCheckConfiguredRejectsNilConfig(t *testing.T) {
	if err := CheckConfigured(nil); err == nil {
		t.Fatal("CheckConfigured(nil) must fail")
	}
}

func TestSaveRejectsMissingFieldAsInvalidConfig(t *testing.T) {
	store := NewConfigStore(writeTestConfig(t, testConfig))
	conf, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	conf.Form.Username = ""

	err = store.Save(conf)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Save: error %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "form.username") {
		t.Errorf("Save: error %q should name the missing field", err)
	}
}
