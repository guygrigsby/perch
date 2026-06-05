// Package client gives an appctl its cobra root and a JSON-over-HTTP client
// to a local daemon.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// Client is a thin JSON-over-HTTP client carrying a base address and an
// optional bearer token.
type Client struct {
	addr  string
	token string
	hc    *http.Client
}

// unixScheme is the addr prefix that selects a unix domain socket, matching
// daemon.UnixScheme on the server side.
const unixScheme = "unix://"

// NewClient builds a Client with a 10s timeout. Trailing slash on addr is
// trimmed so callers pass paths like "/api/whoami". An addr of
// "unix://<path>" dials that unix domain socket; the HTTP host becomes a fixed
// placeholder since the socket path, not the host, selects the daemon.
func NewClient(addr, token string) *Client {
	hc := &http.Client{Timeout: 10 * time.Second}
	if path, ok := strings.CutPrefix(addr, unixScheme); ok {
		hc.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		}
		// Requests need a valid http URL; the host is ignored by the dialer.
		addr = "http://unix"
	}
	return &Client{
		addr:  strings.TrimRight(addr, "/"),
		token: token,
		hc:    hc,
	}
}

// GetJSON GETs path and decodes a 2xx JSON body into out (out may be nil).
func (c *Client) GetJSON(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// PostJSON POSTs body as JSON to path and decodes a 2xx response into out
// (body and out may each be nil).
func (c *Client) PostJSON(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.addr+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("%s %s: %s: %s", method, path, res.Status, strings.TrimSpace(string(b)))
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return fmt.Errorf("%s %s: decode response: %w", method, path, err)
		}
		return nil
	}
	// Drain so the connection can be reused before the deferred Close.
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
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
