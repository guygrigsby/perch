# Perch — design

**Date:** 2026-05-27
**Status:** approved (brainstorm), pending implementation plan
**Module:** `github.com/guygrigsby/perch`

## Context

talon, pluma, and mlx-stack all reimplement the same daemon/CLI scaffold:
a cobra client that talks to a local daemon over HTTP (`--addr`/`--token`,
loopback default, `~/.config/<app>` token file), config loaded from
`~/.config/<app>/config.toml`, and a daemon lifecycle (signal handling,
graceful shutdown). Perch extracts the byte-identical parts into one
imported library so a fix lands once and propagates by version bump.

This is **sub-project 1 of 2**. A separate template repo (GitHub "Use this
template", own spec) encodes the two-binary + optional-web project shape and
*consumes* perch. Perch must exist and carry a tag before the template can
`require` it.

Audience is private grigsby projects, so conventions are opinionated and
hardcoded (`dev.grigsby.*` labels, `~/.config/<app>`, `~/.logs/<app>`). No
OSS placeholder machinery, no retrofit obligation for existing repos.

## Goals

- One importable lib providing the client transport, config loader, and
  daemon lifecycle shared by every grigsby app.
- Each piece small, independently testable, with a clear interface.
- Apps depend on it via go.mod; bug fixes propagate via dep bump.

## Non-goals

- Not a web framework, not HTTP route/handler scaffolding (that's the app).
- Not a server bootstrap or auth-mint endpoint (deferred; medium scope only).
- Not OSS-generic; no templating.
- No automatic retrofit of talon/pluma/mlx-stack.

## Packages

### `perch/client` — CLI-to-daemon transport

What it does: gives an `appctl` its cobra root and a typed HTTP client to a
local daemon. Depends on `cobra`, `net/http`, `encoding/json`, `os`.

```go
// Root builds the cobra root with the shared persistent flags.
// app is the lowercase program id ("pluma"); used for env var and token
// path derivation. defaultAddr is the app's own loopback default
// (apps differ: pluma :8787, talon :18789) — perch takes no port opinion.
func Root(app, short, long, defaultAddr string) (*cobra.Command, *Flags)

type Flags struct { Addr, Token string } // bound to persistent --addr/--token

// ResolveToken returns the bearer token: --token flag wins, then
// <APP>_API_TOKEN env, then ~/.config/<app>/cli.token (mode 0600).
// Returns "", nil when none is found (caller decides if that's fatal).
func ResolveToken(app string, f *Flags) (string, error)

// TokenPath returns ~/.config/<app>/cli.token, honoring XDG_CONFIG_HOME.
func TokenPath(app string) (string, error)

// Client is a thin JSON-over-HTTP client carrying addr + bearer token.
type Client struct { /* addr, token, *http.Client */ }
func NewClient(addr, token string) *Client          // 10s default timeout
func (c *Client) GetJSON(ctx, path string, out any) error
func (c *Client) PostJSON(ctx, path string, body, out any) error
// Non-2xx -> error including status code + response body. Sets
// Authorization: Bearer when token != "".
```

Env var derived as `strings.ToUpper(app)+"_API_TOKEN"` and
`..._ADDR_URL`. Matches pluma's existing `PLUMA_API_TOKEN` /
`PLUMA_ADDR_URL`.

### `perch/config` — config file loader

What it does: loads `~/.config/<app>/config.toml` into an app-defined
struct. Depends on `github.com/BurntSushi/toml`, `os`.

```go
// Dir returns ~/.config/<app>, honoring XDG_CONFIG_HOME.
func Dir(app string) (string, error)

// Load decodes <Dir>/config.toml into v. A missing file is NOT an error:
// v keeps its caller-set defaults. A malformed file IS an error.
func Load(app string, v any) error
```

Save/Write is deferred (YAGNI) until an app needs to persist config.

### `perch/daemon` — lifecycle

What it does: runs an `*http.Server` with signal-driven graceful shutdown.
Depends on `net/http`, `os/signal`, `context`, `time`.

```go
// ResolveAddr picks the listen address: flag value wins, then envVar,
// then the supplied default. (Apps own their default port.)
func ResolveAddr(flagVal, envVar, def string) string

// SignalContext returns a context cancelled on SIGINT/SIGTERM (thin wrapper
// over signal.NotifyContext). Owning signal handling here keeps it identical
// across apps; keeping it OUT of Serve keeps Serve unit-testable with a
// plain cancellable context.
func SignalContext() (context.Context, context.CancelFunc)

// Serve runs srv until ctx is cancelled, then calls srv.Shutdown with the
// given grace timeout. Returns the first non-ErrServerClosed error.
// Typical use: ctx, cancel := daemon.SignalContext(); defer cancel();
//              daemon.Serve(ctx, srv, 10*time.Second).
func Serve(ctx context.Context, srv *http.Server, grace time.Duration) error
```

## Conventions (authoritative; template plist/Makefile follow these)

- Config: `~/.config/<app>/config.toml`
- Token: `~/.config/<app>/cli.token` (mode 0600)
- Logs: `~/.logs/<app>/` (consumed by the template's launchd plist)
- Env: `<APP>_ADDR_URL`, `<APP>_API_TOKEN`
- No hardcoded port: the app passes its own default to `Root`/`ResolveAddr`.

## Layout

```
go.mod                 # module github.com/guygrigsby/perch
client/client.go
client/client_test.go
config/config.go
config/config_test.go
daemon/daemon.go
daemon/daemon_test.go
docs/specs/2026-05-27-perch-design.md
```

Minimal deps: `spf13/cobra`, `BurntSushi/toml`. Go version pinned to match
the consuming apps.

## Testing

- **client:** `ResolveToken` precedence (flag > env > file > none, each in
  isolation via `t.Setenv` + temp HOME); `TokenPath` with and without
  `XDG_CONFIG_HOME`; HTTP helpers against `httptest.Server` — bearer header
  present, non-2xx surfaces status+body, JSON decodes into `out`.
- **config:** `Load` with present / missing / malformed file; `Dir` honors
  `XDG_CONFIG_HOME`.
- **daemon:** `Serve` shuts down within grace on ctx cancel, returns nil on
  clean shutdown, propagates listen errors; `ResolveAddr` precedence
  (flag > env > default). `SignalContext` is thin glue over the stdlib and
  needs no signal-delivery test.

## Out of scope (template repo, separate spec)

`cmd/appd` + `cmd/appctl` skeletons, the Vite+Svelte web module and embed
seam, the Makefile spine + launchd targets + `dev-watch.sh`, and
`scripts/init.sh` renaming.
