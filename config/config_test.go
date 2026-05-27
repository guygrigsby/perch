package config

import (
	"os"
	"path/filepath"
	"testing"
)

type sample struct {
	Listen string `toml:"listen"`
	Debug  bool   `toml:"debug"`
}

// writeConfig points app config at a temp dir via XDG_CONFIG_HOME and writes
// config.toml with the given body. Returns nothing; t.Setenv auto-restores.
func writeConfig(t *testing.T, app, body string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	if body == "" {
		return // leave the file absent
	}
	dir := filepath.Join(root, app)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_Present(t *testing.T) {
	writeConfig(t, "pluma", "listen = \":9000\"\ndebug = true\n")
	cfg := sample{Listen: ":8787"}
	if err := Load("pluma", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9000" || !cfg.Debug {
		t.Errorf("got %+v", cfg)
	}
}

func TestLoad_MissingKeepsDefaults(t *testing.T) {
	writeConfig(t, "pluma", "") // no file
	cfg := sample{Listen: ":8787"}
	if err := Load("pluma", &cfg); err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if cfg.Listen != ":8787" {
		t.Errorf("defaults clobbered: %+v", cfg)
	}
}

func TestLoad_Malformed(t *testing.T) {
	writeConfig(t, "pluma", "listen = = broken\n")
	var cfg sample
	if err := Load("pluma", &cfg); err == nil {
		t.Fatal("expected error on malformed toml, got nil")
	}
}

func TestDir_UsesXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdghome")
	got, err := Dir("pluma")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/xdghome/pluma" {
		t.Errorf("got %q", got)
	}
}
