# dpi-bypass

Simple tool to bypass **DPI / SNI blocking** (e.g. **Steam store & community blocked**, "connection reset") with no driver and no admin rights. A lightweight alternative to GoodbyeDPI / zapret for Windows, with experimental macOS and Linux builds. Single binary, written in Go.

It runs a tiny local proxy that splits the TLS ClientHello so your ISP's DPI can't read the site name, and registers itself as the system proxy (PAC file).

## Install

Download from [Releases](../../releases). Each download is everything you need: the web pages are built into the program, there is nothing else to install.

| OS | Download | Run |
|---|---|---|
| Windows | `dpi-bypass-windows-amd64.exe` | double-click it. A tray icon appears. Windows may warn because the file is not code-signed: **More info > Run anyway** |
| macOS | `brew install hong4rc/tap/dpi-bypass` (recommended), or `dpi-bypass-macos-apple-silicon.tar.gz` / `dpi-bypass-macos-intel.tar.gz` | run `dpi-bypass` in a terminal (experimental). Homebrew avoids the "Apple could not verify" warning. With a manual download, macOS blocks it until you run `xattr -d com.apple.quarantine dpi-bypass` |
| Linux | `dpi-bypass-linux-amd64.tar.gz` | extract, then `./dpi-bypass` in a terminal (experimental) |

macOS and Linux are `.tar.gz` because a plain downloaded binary loses its "executable" permission; the archive keeps it. `SHA256SUMS.txt` lists the checksums.

Its settings, lists and logs are saved next to the program (see "Files" below). If that folder is read-only, they go to your user config folder instead (`%AppData%\dpi-bypass` on Windows), so nothing is silently lost.

Or build it yourself: `just build` / `go build -o dpi.exe ./cmd/dpi-bypass`.

## Use (Windows)

1. Double-click the exe. A dot appears in the system tray (green when the bypass is on). Nothing pops up.
2. Open Steam or your browser. Check: https://store.steampowered.com
3. **Right-click the tray icon** for quick actions: Turn on/off, Share with other devices, Open dashboard, Clear log, Start with Windows, Quit. There are no notifications for normal things (starting, switching something, learning a site): the icon colour, the checkmarks and the dashboard are the feedback. A notification only appears when something really failed, for example when something else is using the port.
4. **Left-click the tray icon** to turn the bypass on or off. For the dashboard, choose Open dashboard, start the exe a second time, or open `http://127.0.0.1:18080/`.

macOS/Linux: run the binary in a terminal, open the dashboard address it prints, stop with Ctrl+C. On Linux without GNOME, set your system proxy auto-config (PAC) URL to `http://127.0.0.1:18080/proxy.pac` yourself.

## Dashboard

For the things that need room to read or edit. Dark Tokyo Night theme, light theme follows your system.

| Tab | What it shows |
|---|---|
| Overview | on/off, the **DPI mode** (Learn by default, or Always), counters (including connections open right now), **Test Steam connection** with the result shown inline, sharing address with phone steps |
| Devices | **Phone setup** (proxy address with a Copy button and a check that it answers, plus a QR code to identify a phone) and who uses the proxy: name, address, MAC, last seen, traffic. Click a name to rename it |
| Activity | the last 2000 connections (the newest 500 are listed, filter and search reach all 2000): bypassed, direct, auto-skipped, learned bypass, failed, with one-click **Skip** (never bypass) and **Bypass** (always bypass) |
| Logs | live log with info/debug filters, search, pause, copy, clear |
| Sites | the two lists of what is known about sites, **Needs bypass** and **Never bypass**: add and remove entries (no text editor needed). Also shows where each entry came from |

Only this PC can open the dashboard: other devices get a 403, the page must be opened as `127.0.0.1` or `localhost`, and every change needs a header other websites can't send.

**Identify a device (real model):** with sharing on, the PC also serves a small HTTPS page, `https://<PC-IP>:18443/identify`. Open it on the phone in Chrome (the Devices tab shows a QR code and the address). The browser warns that the page is not private, because the PC made its own certificate: tap **Advanced > Proceed**. The page sends the phone's model and OS to the PC, so the device shows its real name, for example `SHARK PAR-A0 · Android 12`, and it tells you whether the phone is using the proxy. If it is not, the page shows the proxy address with a **Copy proxy address** button.

Why the warning: browsers only give the model to HTTPS pages, and nothing trusts a certificate the PC made, so it shows the first time. The certificate is kept in `identify-cert.pem` and renews itself. Skip this step if you prefer and rename the device in the dashboard. No phone setting is changed. Safari and Firefox keep the model private, so use Chrome.

**Device names:** your own name first, then what the identify page reported, then the hostname, then what the device says in its User-Agent. Hover a device in the Devices tab for the details. Phones behind a second router share one address and show up as one device.

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
- The PAC falls back to a direct connection, and only `https://` traffic to public sites is sent through the proxy. LAN addresses, plain names and local-only names (`.local`, `.lan`, `.internal` such as Docker's `host.docker.internal`, `.home.arpa`, `.localhost`, `.test`) always go direct, and the proxy never fragments a connection whose name resolves to a LAN address.
- Still stuck? Run `dpi-bypass -restore`.

## DPI modes: Always or Learn

**Learn is the default.** Choose in the dashboard (Overview > DPI mode). The mode only decides what happens for sites **nothing is known about yet**; it never changes what has been learned, so you can switch back and forth freely.

| | Unknown site | Site known to need the bypass | Site known to work better without it |
|---|---|---|---|
| **Always** | bypass first; if that fails and a normal connection works, that is evidence it should connect directly | bypass | direct |
| **Learn** | connect normally first; if that fails and the bypass works, that is evidence it needs the bypass | bypass | direct |

Both modes read and write the same two lists, which are the only place anything is remembered:

| State | Meaning | Stored in |
|---|---|---|
| `NO_BYPASS` | connect directly, never bypass. Wins over everything else, in both modes | `blacklist.txt` (the old skip list, same name, format and meaning) |
| `NEEDS_BYPASS` | always use the bypass | `bypass.txt` (new, same format) |
| `UNKNOWN` | nothing known: follow the mode | not stored at all |

- **No spam:** visiting sites that work never writes anything. A site only appears in a list when the app has real evidence, or when you add it. Nothing is pre-filled, so a new install starts with empty lists.
- **A hiccup is not evidence:** a decision is only written after the same failure-then-fix was seen **twice within 10 minutes**. A normal connection that works in between cancels the pending evidence. The connection itself is served either way; only the permanent note waits. What counts as a failure: no reply, a reset, or a timeout of the first attempt. A TLS alert from a server that gets an unfragmented hello is a real answer (the server refused), not a failure.
- **A learned decision is not flipped by one contradiction:** for a site known to need the bypass, a failing bypass falls back to a direct connection for that one connection, and the list is left alone. Edit the list in the dashboard if a site really changed.
- **Learn mode and the proxy:** every HTTPS request still goes through the proxy in Learn mode, because the proxy has to see a failure to learn from it. It just leaves unknown sites untouched until one fails.
- Learned sites are shown in the **Sites** tab and written to `dpi-info.log`; they never pop up a notification.

## Leaving it running for months

The app is meant to run for a very long time without a restart:

- **Bounded memory:** Activity keeps the last 2000 connections and Logs the last 3000 lines; counters are plain numbers; the log files roll over at 5 MB and 20 MB, so they use at most twice that on disk.
- **No stuck connections:** a client that opens a tunnel and never sends anything is dropped after 30 seconds, dead peers are detected by TCP keep-alive, and web requests that never finish are cut off.
- **Sharing follows the network:** if the router gives the PC a new address, Wi-Fi reconnects, or the app started before the network was up, sharing moves to the PC's current address within 30 seconds (only while sharing is on; the setup address changes with it).
- **The identify certificate renews itself** before it expires, without a restart.

## A game or app breaks? (e.g. League of Legends)

- **Automatic:** in Always mode the app learns `NO_BYPASS` when the bypass keeps breaking a site that works normally. Check the **Sites** tab. Learn mode (the default) starts from a normal connection and does not touch sites that already work.
- **Manual:** add the domain as **Never bypass** in the dashboard's Sites tab (or one per line in `blacklist.txt`). A domain also covers its subdomains. Edits to either file apply within 2 seconds, no restart.

## Files (next to the program)

| File | Content |
|---|---|
| `blacklist.txt` | sites that never use the bypass (`NO_BYPASS`), also editable in the dashboard |
| `bypass.txt` | sites that need the bypass (`NEEDS_BYPASS`), also editable in the dashboard |
| `settings.json` | saved choices, for example `{"lanSharing": true, "dpiMode": "learn"}` |
| `identify-cert.pem` | the identify page's self-signed certificate and key (only for that page; delete it and a new one is made) |
| `devices.json` | names you gave to devices and what phones reported on the identify page |
| `dpi-info.log` | start/stop, failures, `AUTO-SKIP`, `AUTO-BYPASS`, `NO REPLY` (recreated on every start; at 5 MB it becomes `dpi-info.log.old` and a new one starts) |
| `dpi-debug.log` | everything: every CONNECT, timing, bytes (recreated on every start; at 20 MB it becomes `dpi-debug.log.old` and a new one starts) |

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

Layout: `cmd/dpi-bypass` (entry), `internal/policy` (modes, decisions, evidence), `internal/proxy` (split, retries, PAC), `internal/dashboard` (web UI + API), `internal/stats` (devices, identities, activity), `internal/identify` (HTTPS identify page), `internal/blacklist`, `internal/settings`, `internal/sysproxy` (Windows / macOS / Linux + crash guard), `internal/tray` (Windows icon and menu), `internal/autostart`, `internal/app`, `internal/notify`, `internal/logx`.
