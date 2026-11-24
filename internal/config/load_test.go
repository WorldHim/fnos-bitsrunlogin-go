package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Mmx233/BitSrunLoginGo/internal/config"
)

// The package init already ran by the time these tests execute, so the tests
// drive Load directly rather than relying on process start-up behaviour.

func TestLoadCreatesDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Config.yaml")

	err := config.Load(path, true)
	if !errors.Is(err, config.ErrConfigCreated) {
		t.Fatalf("expected ErrConfigCreated, got %v", err)
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("config file was not created: %v", statErr)
	}
	// Windows does not model POSIX permission bits: the mode comes back as 0666
	// no matter what was requested, so the check only makes sense elsewhere.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("config file permission = %o, want 600", perm)
		}
	}
	if info.Size() == 0 {
		t.Error("config file is empty")
	}
}

func TestLoadMissingWithoutCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Config.yaml")

	err := config.Load(path, false)
	if err == nil {
		t.Fatal("expected an error for a missing config file")
	}
	if errors.Is(err, config.ErrConfigCreated) {
		t.Fatal("must not report ErrConfigCreated when create is false")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("Load must not create the file when create is false")
	}
}

func TestLoadReadsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Config.yaml")

	if err := config.Load(path, true); !errors.Is(err, config.ErrConfigCreated) {
		t.Fatalf("setup failed: %v", err)
	}

	if err := config.Load(path, false); err != nil {
		t.Fatalf("loading an existing config failed: %v", err)
	}

	if config.Form == nil || config.Meta == nil || config.Settings == nil {
		t.Fatal("config variables were not populated")
	}
	if config.Form.Domain != "www.msftconnecttest.com" {
		t.Errorf("Form.Domain = %q, want the default value", config.Form.Domain)
	}
	if config.Settings.Guardian.Duration != 300 {
		t.Errorf("Guardian.Duration = %d, want 300", config.Settings.Guardian.Duration)
	}
	if config.Timeout == 0 {
		t.Error("Timeout was not derived from Basic.Timeout")
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Config.yaml")

	if err := os.WriteFile(path, []byte("form: [this is not valid"), 0600); err != nil {
		t.Fatal(err)
	}

	err := config.Load(path, false)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if errors.Is(err, config.ErrConfigCreated) {
		t.Fatal("a parse failure must not be reported as ErrConfigCreated")
	}
}
