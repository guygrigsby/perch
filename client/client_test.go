package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenPath_XDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdghome")
	got, err := TokenPath("pluma")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/tmp/xdghome/pluma/cli.token"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveToken_FlagWins(t *testing.T) {
	t.Setenv("PLUMA_API_TOKEN", "fromenv")
	tok, err := ResolveToken("pluma", &Flags{Token: "fromflag"})
	if err != nil {
		t.Fatal(err)
	}
	if tok != "fromflag" {
		t.Errorf("got %q, want fromflag", tok)
	}
}

func TestResolveToken_EnvBeatsFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PLUMA_API_TOKEN", "fromenv")
	dir := filepath.Join(root, "pluma")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli.token"), []byte("fromfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := ResolveToken("pluma", &Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if tok != "fromenv" {
		t.Errorf("got %q, want fromenv", tok)
	}
}

func TestResolveToken_FileWhenNoFlagOrEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PLUMA_API_TOKEN", "")
	dir := filepath.Join(root, "pluma")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli.token"), []byte("fromfile\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := ResolveToken("pluma", &Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if tok != "fromfile" { // trailing CR/LF trimmed
		t.Errorf("got %q, want fromfile", tok)
	}
}

func TestResolveToken_NoneIsEmptyNotError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PLUMA_API_TOKEN", "")
	tok, err := ResolveToken("pluma", &Flags{})
	if err != nil {
		t.Fatalf("missing token must not error: %v", err)
	}
	if tok != "" {
		t.Errorf("got %q, want empty", tok)
	}
}
