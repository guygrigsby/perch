package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
)

func TestClient_UnixSocketTransport(t *testing.T) {
	// Short relative socket name: macOS caps sun_path at 104 bytes, which the
	// long t.TempDir() absolute path would exceed.
	t.Chdir(t.TempDir())
	sock := "c.sock"
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer func() { _ = ln.Close() }()

	var gotAuth string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "pip"})
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	c := NewClient("unix://"+sock, "sekret")
	var out struct{ Name string }
	if err := c.GetJSON(context.Background(), "/whoami", &out); err != nil {
		t.Fatalf("GetJSON over socket: %v", err)
	}
	if out.Name != "pip" {
		t.Errorf("decoded %+v", out)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("auth header = %q, want Bearer sekret", gotAuth)
	}
}
