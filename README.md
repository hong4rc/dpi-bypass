# dpi-bypass

Simple tool to bypass **DPI / SNI blocking** (e.g. **Steam store & community blocked**, "connection reset") with no driver and no admin rights. A lightweight alternative to GoodbyeDPI / zapret for Windows, with experimental macOS and Linux builds. Single binary, written in Go.

It runs a tiny local proxy that splits the TLS ClientHello so your ISP's DPI can't read the site name, and registers itself as the system proxy (PAC file).

## Install

Download from [Releases](../../releases):

| OS | File |
|---|---|
| Windows | `dpi-bypass-windows-amd64.exe` (tray icon, no console) |
| macOS | `dpi-bypass-macos-apple-silicon` / `dpi-bypass-macos-intel` (terminal, experimental) |
| Linux | `dpi-bypass-linux-amd64` (terminal, GNOME auto-setup, experimental) |

Or build: `just build` / `go build -o dpi.exe ./cmd/dpi-bypass`.

## Use (Windows)

1. Double-click the exe. A green dot appears in the system tray and a notification says it is ON.
2. Open Steam or your browser. Check: https://store.steampowered.com
3. Tray menu: **Turn off/on**, **Test Steam connection**, **Open log**, **Edit skip list**, **Start with Windows**, **Quit**.

macOS/Linux: run the binary in a terminal and stop it with Ctrl+C. On Linux without GNOME, set your system proxy auto-config (PAC) URL to `http://127.0.0.1:18080/proxy.pac` yourself.

## It will not lock you out

- Quitting, Ctrl+C or a normal close restores your previous proxy setting.
- A hidden guard process (Windows) restores it even if the app crashes or is killed.
- The PAC falls back to a direct connection, and only `https://` traffic is sent through the proxy.
- Still stuck? Run `dpi-bypass -restore`.

## A game or app breaks? (e.g. League of Legends)

- **Automatic:** if a site gets no reply with the split hello but works without it, the app retries without splitting and adds the host to `blacklist.txt`, with a notification.
- **Manual:** put domains in `blacklist.txt` (next to the exe, created on first run) to connect **directly**. A domain also covers its subdomains. Edits apply within 2 seconds, no restart.

```
riotgames.com
riotcdn.net
```

## Logs

Recreated on every start, next to the exe:

| File | Content |
|---|---|
| `dpi-info.log` | start/stop, failures, `AUTO-SKIP`, `NO REPLY` |
| `dpi-debug.log` | everything: every CONNECT, timing, bytes up/down |

## Limits

- Only fixes **SNI-based** blocking over HTTPS. Not IP or DNS blocking, not UDP/QUIC.
- Only apps that honor the system proxy are covered (browsers, Steam client, most launchers).
- macOS and Linux builds are untested on real machines and have no tray icon.

## Develop

```
just build              # vet + test + build dpi.exe (tray, no console)
just run                # run from source with console output
go test ./...
```

Layout: `cmd/dpi-bypass` (entry), `internal/proxy` (split + auto-skip + PAC), `internal/blacklist`, `internal/sysproxy` (Windows / macOS / Linux + crash guard), `internal/tray` (Windows UI), `internal/app`, `internal/notify`, `internal/logx`.
