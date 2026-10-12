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

1. Double-click the exe. A dot appears in the system tray and a notification says it is ON.
2. Open Steam or your browser. Check: https://store.steampowered.com
3. **Right-click the tray icon** for quick actions: Turn on/off, Share with other devices, Open dashboard, Clear log, Start with Windows, Quit. The menu never pops up notifications: the icon colour and the checkmarks are the feedback. The only notifications are the "ON" message at start, one summary when new sites are added to the skip list, and real errors.
4. **Left-click the tray icon** (or open `http://127.0.0.1:18080/`) for the dashboard.

macOS/Linux: run the binary in a terminal, open the dashboard address it prints, stop with Ctrl+C. On Linux without GNOME, set your system proxy auto-config (PAC) URL to `http://127.0.0.1:18080/proxy.pac` yourself.

## Dashboard

For the things that need room to read or edit. Dark Tokyo Night theme, light theme follows your system.

| Tab | What it shows |
|---|---|
| Overview | on/off, counters (including connections open right now), **Test Steam connection** with the result shown inline, sharing address with phone steps |
| Devices | who uses the proxy: name, address, MAC, last seen, traffic. Click a name to rename it |
| Activity | recent connections: bypassed, direct, auto-skipped, failed, with a one-click **Skip** |
| Logs | live log with info/debug filters, search, pause, copy, clear |
| Skip list | add and remove domains (no text editor needed) |

Only this PC can open the dashboard: other devices get a 403, the page must be opened as `127.0.0.1` or `localhost`, and every change needs a header other websites can't send.

**Device names:** a device is named by you first, then by its hostname if the network knows it, then by what it says it is in its User-Agent (for example `Android 14 · SM-S918B` or `iPhone14,5 · iOS 17.2`). Hover a device name to see the raw User-Agent headers it sent; each distinct header is also written once to `dpi-debug.log` (`device 192.168.1.13 says: ...`). Chrome on Android and Safari hide the exact model in web traffic, so the model only shows when the phone's system or an app sends it (the system fetch of the proxy address often does). Otherwise rename the device once. Phones behind a second router (NAT) share one address and show up as one device with several descriptions.

## Use on iPhone, Android and other devices

Your PC can share the bypass with every device on the same Wi-Fi/router.

1. Tray menu: tick **Share with other devices** (or use the switch in the dashboard). The address `http://<PC-IP>:18080/proxy.pac` is copied for you and shown in the dashboard. Without a tray: run with `-lan` (remembered; `-no-lan` turns it off).
2. Windows asks about the firewall the first time: allow **Private networks**. If you missed it, run once as admin: `netsh advfirewall firewall add rule name="dpi-bypass" dir=in action=allow protocol=TCP localport=18080 profile=private`
3. On the device, set the Wi-Fi proxy to **Automatic** and paste the address:
   - iPhone: Settings > Wi-Fi > (i) > Configure Proxy > Automatic
   - Android: Wi-Fi > network > Advanced > Proxy > Proxy Auto-Config
   - Mac / other PC: Network > Proxy > Automatic proxy configuration
4. Reserve a fixed IP for the PC in your router (DrayTek: LAN > Bind IP to MAC), or the address changes.

Safety: only private LAN addresses can connect, and a device on the LAN cannot use the proxy to reach services running on your PC itself. The PC must stay on and awake; if it is off, devices that never loaded the PAC just connect normally, and ones that cached it may wait a few seconds before going direct. Router-wide setup: DHCP option 252 (WPAD) with the same address, if your router allows it.

## It will not lock you out

- Quitting, Ctrl+C or a normal close restores your previous proxy setting.
- A hidden guard process (Windows) restores it even if the app crashes or is killed.
- The PAC falls back to a direct connection, and only `https://` traffic to public sites is sent through the proxy (LAN addresses go direct).
- Still stuck? Run `dpi-bypass -restore`.

## A game or app breaks? (e.g. League of Legends)

- **Automatic:** if a site gets no reply (or a TLS alert) with the split hello but works without it, the app retries without splitting and adds the host to the skip list. You get **one** notification per batch of newly skipped sites, never one per connection.
- **Manual:** add domains in the dashboard's Skip list, or in `blacklist.txt` next to the exe. A domain also covers its subdomains. Edits apply within 2 seconds, no restart.

## Files next to the exe

| File | Content |
|---|---|
| `blacklist.txt` | the skip list (also editable in the dashboard) |
| `settings.json` | saved choices, for example `{"lanSharing": true}` |
| `device-names.json` | names you gave to devices |
| `dpi-info.log` | start/stop, failures, `AUTO-SKIP`, `NO REPLY` (recreated on every start) |
| `dpi-debug.log` | everything: every CONNECT, timing, bytes (recreated on every start) |

"Clear log" (tray or dashboard) empties both log files.

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

Layout: `cmd/dpi-bypass` (entry), `internal/proxy` (split + auto-skip + PAC), `internal/dashboard` (web UI + API), `internal/stats` (devices, activity), `internal/blacklist`, `internal/settings`, `internal/sysproxy` (Windows / macOS / Linux + crash guard), `internal/tray` (Windows icon and menu), `internal/autostart`, `internal/app`, `internal/notify`, `internal/logx`.
