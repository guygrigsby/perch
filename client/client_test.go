package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestRoot_AddrDefaultsToArg(t *testing.T) {
	t.Setenv("PLUMA_ADDR_URL", "")
	root, f := Root("pluma", "short", "long", "http://127.0.0.1:8787")
	if err := root.PersistentFlags().Parse(nil); err != nil {
		t.Fatal(err)
	}
	if f.Addr != "http://127.0.0.1:8787" {
		t.Errorf("got %q, want the default", f.Addr)
	}
	if root.Use != "pluma" {
		t.Errorf("Use = %q", root.Use)
	}
}

func TestRoot_AddrEnvOverridesDefault(t *testing.T) {
	t.Setenv("PLUMA_ADDR_URL", "http://10.0.0.5:8787")
	_, f := Root("pluma", "short", "long", "http://127.0.0.1:8787")
	// env folds into the flag default; with no --addr passed, f.Addr is env.
	if f.Addr != "http://10.0.0.5:8787" {
		t.Errorf("got %q, want env value", f.Addr)
	}
}

func TestRoot_AddrFlagOverridesEnv(t *testing.T) {
	t.Setenv("PLUMA_ADDR_URL", "http://10.0.0.5:8787")
	root, f := Root("pluma", "short", "long", "http://127.0.0.1:8787")
	if err := root.PersistentFlags().Parse([]string{"--addr", "http://1.2.3.4:9999"}); err != nil {
		t.Fatal(err)
	}
	if f.Addr != "http://1.2.3.4:9999" {
		t.Errorf("got %q, want flag value", f.Addr)
	}
}

func TestClient_GetJSONDecodesAndSendsBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "pip"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "sekret")
	var out struct{ Name string }
	if err := c.GetJSON(context.Background(), "/whoami", &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "pip" {
		t.Errorf("decoded %+v", out)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("auth header = %q", gotAuth)
	}
}

func TestClient_PostJSONSendsBody(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	if err := c.PostJSON(context.Background(), "/echo", map[string]string{"hi": "there"}, nil); err != nil {
		t.Fatal(err)
	}
	if gotBody["hi"] != "there" {
		t.Errorf("server saw %+v", gotBody)
	}
}

func TestClient_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	err := c.GetJSON(context.Background(), "/x", nil)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should carry status + body: %v", err)
	}
}

func TestClient_NoTokenNoAuthHeader(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	if err := c.GetJSON(context.Background(), "/x", nil); err != nil {
		t.Fatal(err)
	}
	if hadAuth {
		t.Error("no token must mean no Authorization header")
	}
}

func TestClient_DecodeErrorIsWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is not json"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	var out struct{ Name string }
	err := c.GetJSON(context.Background(), "/x", &out)
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
	if !strings.Contains(err.Error(), "GET /x") || !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error should carry method/path + decode context: %v", err)
	}
}
