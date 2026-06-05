package daemon

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// tmpSock returns a short relative socket name after chdir-ing into a temp
// dir. macOS caps a unix socket path (sun_path) at 104 bytes, which the long
// t.TempDir() absolute paths blow past; a relative name keeps it tiny.
func tmpSock(t *testing.T) string {
	t.Helper()
	t.Chdir(t.TempDir())
	return "d.sock"
}

func TestListen_UnixSocket(t *testing.T) {
	sock := tmpSock(t)
	ln, err := Listen("unix://" + sock)
	if err != nil {
		t.Fatalf("Listen unix: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if got := ln.Addr().Network(); got != "unix" {
		t.Errorf("network = %q, want unix", got)
	}
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("socket not created: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket perm = %o, want 600", perm)
	}
}

func TestListen_RemovesStaleSocket(t *testing.T) {
	sock := tmpSock(t)
	// A leftover file from a crashed prior run must not block bind.
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen("unix://" + sock)
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	defer func() { _ = ln.Close() }()
}

func TestListen_TCP(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen tcp: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if got := ln.Addr().Network(); got != "tcp" {
		t.Errorf("network = %q, want tcp", got)
	}
}

func TestServeListener_GracefulShutdownOnCtxCancel(t *testing.T) {
	sock := tmpSock(t)
	ln, err := Listen("unix://" + sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NewServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeListener(ctx, srv, ln, time.Second) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("clean shutdown should return nil, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeListener did not return after ctx cancel")
	}
	// SetUnlinkOnClose: graceful shutdown should remove the socket file.
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket file should be unlinked after close, stat err = %v", err)
	}
}
