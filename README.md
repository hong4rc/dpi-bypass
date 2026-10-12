# dpi-bypass

Simple Windows tool to bypass **DPI / SNI blocking** (e.g. **Steam store & community blocked**, "connection reset") without a driver or admin rights. A lightweight, easy alternative to GoodbyeDPI and WinDivert tools.

It runs a tiny local proxy that splits the TLS ClientHello so your ISP's DPI can't read the site name, then registers itself as the Windows proxy (PAC file). Single `.exe`, written in Go.

## Use

1. Download `dpi.exe` (Actions → latest run → artifact `dpi-windows`) or build it: `just build` / `go build -o dpi.exe .`
2. Double-click `dpi.exe` and keep the window open.
3. Open Steam / your browser. Check: https://store.steampowered.com
4. Close the window (or Ctrl+C) to stop. Proxy settings are restored automatically.

If it was force-killed and the proxy setting stays behind: `dpi.exe -restore`.

## Game or app breaks? (e.g. League of Legends)

Some apps dislike proxies. Put their domains in `blacklist.txt` (next to the exe, created on first run) so they connect **directly**:

```
riotgames.com
riotcdn.net
```

A domain also covers its subdomains. Restart `dpi.exe` after editing.

## Logs

Recreated on every start, next to the exe:

| File | Content |
|---|---|
| `dpi-info.log` | start/stop, failures, `NO REPLY from <host>` (likely blocked/reset) |
| `dpi-debug.log` | everything: every CONNECT, timing, bytes up/down |

Host failing? Look for `FAIL` / `NO REPLY` lines, then add that host to `blacklist.txt`.

## Limits

- Only fixes **SNI-based** blocking over HTTPS. Not IP or DNS blocking, not UDP/QUIC.
- Only apps that honor the Windows proxy are covered (browsers, Steam client, most launchers).
- Windows only.

## Develop

```
just build   # vet + build
go test ./...
```
