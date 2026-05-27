# perch

Shared foundation for grigsby daemon/CLI apps (talon, pluma, …). Three small
packages, imported — not copy-pasted — so a fix lands once.

- `client` — cobra root with `--addr`/`--token`, `ResolveToken`
  (flag → `<APP>_API_TOKEN` → `~/.config/<app>/cli.token`), and a
  JSON-over-HTTP `Client` (`GetJSON`/`PostJSON`, bearer auth).
- `config` — `Load(app, &cfg)` from `~/.config/<app>/config.toml`
  (missing file keeps defaults).
- `daemon` — `ResolveAddr`, `SignalContext`, and a graceful `Serve`.

## Conventions

- Config: `~/.config/<app>/config.toml`
- Token: `~/.config/<app>/cli.token` (mode 0600)
- Env: `<APP>_ADDR_URL`, `<APP>_API_TOKEN`
- No hardcoded port: the app passes its own default.

## Example

```go
root, f := client.Root("pluma", "Pluma CLI", "Talks to a running pluma.", "http://127.0.0.1:8787")
// add subcommands to root, then in a RunE:
tok, _ := client.ResolveToken("pluma", f)
c := client.NewClient(f.Addr, tok)
var who struct{ Name string }
_ = c.GetJSON(cmd.Context(), "/api/whoami", &who)
```
