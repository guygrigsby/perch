package xdg

import (
	"path/filepath"
	"testing"
)

func TestConfigDir_XDGWins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdghome")
	got, err := ConfigDir("pluma")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/tmp/xdghome/pluma"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConfigDir_HomeFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/fakehome")
	got, err := ConfigDir("pluma")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/tmp/fakehome", ".config", "pluma"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
