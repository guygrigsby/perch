package daemon

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestResolveAddr(t *testing.T) {
	t.Setenv("PLUMA_ADDR", "envaddr")
	cases := []struct {
		name, flag, env, def, want string
	}{
		{"flag wins", "flagaddr", "PLUMA_ADDR", ":8787", "flagaddr"},
		{"env when no flag", "", "PLUMA_ADDR", ":8787", "envaddr"},
		{"default when no flag/env", "", "MISSING_VAR", ":8787", ":8787"},
		{"empty envVar name falls to default", "", "", ":8787", ":8787"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveAddr(tc.flag, tc.env, tc.def); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServe_GracefulShutdownOnCtxCancel(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: http.NewServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, time.Second) }()

	time.Sleep(50 * time.Millisecond) // let ListenAndServe bind
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("clean shutdown should return nil, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancel")
	}
}

func TestServe_ListenErrorReturned(t *testing.T) {
	// Port 99999 is out of range; ListenAndServe fails immediately.
	srv := &http.Server{Addr: "127.0.0.1:99999", Handler: http.NewServeMux()}
	err := Serve(context.Background(), srv, time.Second)
	if err == nil {
		t.Fatal("expected listen error, got nil")
	}
}
