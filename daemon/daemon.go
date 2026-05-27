// Package daemon runs an http.Server with signal-driven graceful shutdown.
package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ResolveAddr picks the listen address: flagVal wins, then the value of the
// envVar named by envVar, then def. (Apps own their default port.)
func ResolveAddr(flagVal, envVar, def string) string {
	if flagVal != "" {
		return flagVal
	}
	if envVar != "" {
		if v := os.Getenv(envVar); v != "" {
			return v
		}
	}
	return def
}

// SignalContext returns a context cancelled on SIGINT/SIGTERM. Owning signal
// handling here keeps it identical across apps; keeping it out of Serve keeps
// Serve unit-testable with a plain cancellable context.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Serve runs srv until ctx is cancelled, then calls srv.Shutdown with the
// given grace timeout. Returns the first non-ErrServerClosed error.
//
//	ctx, cancel := daemon.SignalContext()
//	defer cancel()
//	err := daemon.Serve(ctx, srv, 10*time.Second)
func Serve(ctx context.Context, srv *http.Server, grace time.Duration) error {
	errc := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()

	select {
	case err := <-errc:
		// Server stopped on its own (e.g. listen error).
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			return err
		}
		return <-errc // drain ListenAndServe's return (nil after clean close)
	}
}
