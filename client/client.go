// Package client gives an appctl its cobra root and a JSON-over-HTTP client
// to a local daemon.
package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guygrigsby/perch/internal/xdg"
	"github.com/spf13/cobra"
)

// Flags holds the host-targeting flags bound to the cobra root's persistent
// flag set: where the daemon is, and the bearer token to send.
type Flags struct {
	Addr  string
	Token string
}

// TokenPath returns <config dir>/cli.token for app (XDG-aware).
func TokenPath(app string) (string, error) {
	dir, err := xdg.ConfigDir(app)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cli.token"), nil
}

// ResolveToken returns the bearer token: --token flag wins, then the
// <APP>_API_TOKEN env, then <config dir>/cli.token (trailing CR/LF trimmed).
// Returns "", nil when none is found; the caller decides if that's fatal.
func ResolveToken(app string, f *Flags) (string, error) {
	if f != nil && f.Token != "" {
		return f.Token, nil
	}
	if t := os.Getenv(envKey(app, "API_TOKEN")); t != "" {
		return t, nil
	}
	path, err := TokenPath(app)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// envKey builds the <APP>_<SUFFIX> environment variable name.
func envKey(app, suffix string) string {
	return strings.ToUpper(app) + "_" + suffix
}

// Root builds the cobra root command with shared --addr/--token persistent
// flags. app is the lowercase program id (drives env-var names and the token
// path). defaultAddr is the app's own loopback default; perch holds no port
// opinion. The returned *Flags is populated when the command tree parses.
func Root(app, short, long, defaultAddr string) (*cobra.Command, *Flags) {
	f := &Flags{}
	root := &cobra.Command{
		Use:           app,
		Short:         short,
		Long:          long,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addrDefault := os.Getenv(envKey(app, "ADDR_URL"))
	if addrDefault == "" {
		addrDefault = defaultAddr
	}
	pf := root.PersistentFlags()
	pf.StringVar(&f.Addr, "addr", addrDefault, app+" server URL")
	pf.StringVar(&f.Token, "token", os.Getenv(envKey(app, "API_TOKEN")),
		"bearer token (defaults to "+envKey(app, "API_TOKEN")+" or the cli.token file)")
	return root, f
}
