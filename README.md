# perch

Shared foundation for grigsby daemon/CLI apps (talon, pluma, and friends).
Small packages you import instead of copy-pasting, so a fix lands once.

- `client`: cobra root with `--addr`/`--token`, `ResolveToken`
  (flag → `<APP>_API_TOKEN` → `~/.config/<app>/cli.token`), and a
  JSON-over-HTTP `Client` (`GetJSON`/`PostJSON`, bearer auth).
- `config`: `Load(app, &cfg)` from `~/.config/<app>/config.toml`
  (missing file keeps defaults).
- `daemon`: `ResolveAddr`, `SignalContext`, and a graceful `Serve`.
- `telemetry`: OTLP/HTTP metrics from the standard `OTEL_*` variables, a
  no-op when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset. Only the labels the app
  allows are exported, every histogram is exponential (at most 40 buckets)
  and the export interval is never under a minute, which keeps an app inside
  Grafana Cloud's included series and data points. Lifted from sockd.

## Conventions

- Config: `~/.config/<app>/config.toml`
- Token: `~/.config/<app>/cli.token` (mode 0600)
- Env: `<APP>_ADDR_URL`, `<APP>_API_TOKEN`
- No hardcoded port: the app passes its own default.

## Example

```go
root, f := client.Root(
	"pluma", "Pluma CLI", "Talks to a running pluma.",
	"http://127.0.0.1:8787",
)
// Add subcommands to root, then inside a command's RunE:
tok, _ := client.ResolveToken("pluma", f)
c := client.NewClient(f.Addr, tok)

var who struct{ Name string }
_ = c.GetJSON(ctx, "/api/whoami", &who)
```
