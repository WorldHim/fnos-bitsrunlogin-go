package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Mmx233/BitSrunLoginGo/internal/config"
	"github.com/Mmx233/BitSrunLoginGo/internal/config/flags"
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

// writeConfig writes a minimal configuration with the given acid and enc.
func writeConfig(t *testing.T, acid, enc string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Config.yaml")
	body := "form:\n  domain: portal.example.com\n  username: alice\n  password: secret\n" +
		"meta:\n  acid: \"" + acid + "\"\n  enc: \"" + enc + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadEnablesDetectionForEmptyMeta(t *testing.T) {
	defer func(acid, enc bool) {
		flags.AutoAcid, flags.AutoEnc = acid, enc
	}(flags.AutoAcid, flags.AutoEnc)
	flags.AutoAcid, flags.AutoEnc = false, false

	if err := config.Load(writeConfig(t, "", ""), false); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !flags.AutoAcid {
		t.Error("an empty acid must turn detection on")
	}
	if !flags.AutoEnc {
		t.Error("an empty enc must turn detection on")
	}
}

func TestLoadKeepsConfiguredMeta(t *testing.T) {
	defer func(acid, enc bool) {
		flags.AutoAcid, flags.AutoEnc = acid, enc
	}(flags.AutoAcid, flags.AutoEnc)
	flags.AutoAcid, flags.AutoEnc = false, false

	if err := config.Load(writeConfig(t, "5", "srun_bx1"), false); err != nil {
		t.Fatalf("load: %v", err)
	}
	if flags.AutoAcid {
		t.Error("a configured acid must not trigger detection")
	}
	if flags.AutoEnc {
		t.Error("a configured enc must not trigger detection")
	}
	if config.Meta.Acid != "5" || config.Meta.Enc != "srun_bx1" {
		t.Errorf("meta = %q/%q, want the configured values", config.Meta.Acid, config.Meta.Enc)
	}
}

func TestLoadDetectsOnlyTheEmptyField(t *testing.T) {
	defer func(acid, enc bool) {
		flags.AutoAcid, flags.AutoEnc = acid, enc
	}(flags.AutoAcid, flags.AutoEnc)
	flags.AutoAcid, flags.AutoEnc = false, false

	if err := config.Load(writeConfig(t, "5", ""), false); err != nil {
		t.Fatalf("load: %v", err)
	}
	if flags.AutoAcid {
		t.Error("acid was configured, detection must stay off")
	}
	if !flags.AutoEnc {
		t.Error("enc was empty, detection must be on")
	}
}
