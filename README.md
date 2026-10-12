# dpi-bypass

Simple tool to bypass **DPI / SNI blocking** (e.g. **Steam store & community blocked**, "connection reset") with no driver and no admin rights. A lightweight alternative to GoodbyeDPI / zapret for Windows, with experimental macOS and Linux builds. Single binary, written in Go.

It runs a tiny local proxy that splits the TLS ClientHello so your ISP's DPI can't read the site name, and registers itself as the system proxy (PAC file).

## Install

Download from [Releases](../../releases). Each download is everything you need: the web pages are built into the program, there is nothing else to install.

| OS | Download | Run |
|---|---|---|
| Windows | `dpi-bypass-windows-amd64.exe` | double-click it. A tray icon appears. Windows may warn because the file is not code-signed: **More info > Run anyway** |
| macOS | `dpi-bypass-macos-apple-silicon.tar.gz` or `dpi-bypass-macos-intel.tar.gz` | extract, then `./dpi-bypass` in a terminal (experimental). If macOS blocks it: `xattr -d com.apple.quarantine dpi-bypass` |
| Linux | `dpi-bypass-linux-amd64.tar.gz` | extract, then `./dpi-bypass` in a terminal (experimental) |

macOS and Linux are `.tar.gz` because a plain downloaded binary loses its "executable" permission; the archive keeps it. `SHA256SUMS.txt` lists the checksums.

Its settings, lists and logs are saved next to the program (see "Files" below). If that folder is read-only, they go to your user config folder instead (`%AppData%\dpi-bypass` on Windows), so nothing is silently lost.

Or build it yourself: `just build` / `go build -o dpi.exe ./cmd/dpi-bypass`.

## Use (Windows)

1. Double-click the exe. A dot appears in the system tray (green when the bypass is on). Nothing pops up.
2. Open Steam or your browser. Check: https://store.steampowered.com
3. **Right-click the tray icon** for quick actions: Turn on/off, Share with other devices, Open dashboard, Clear log, Start with Windows, Quit. There are no notifications for normal things (starting, switching something, learning a site): the icon colour, the checkmarks and the dashboard are the feedback. A notification only appears when something really failed, for example when another copy is already running.
4. **Left-click the tray icon** (or open `http://127.0.0.1:18080/`) for the dashboard.

macOS/Linux: run the binary in a terminal, open the dashboard address it prints, stop with Ctrl+C. On Linux without GNOME, set your system proxy auto-config (PAC) URL to `http://127.0.0.1:18080/proxy.pac` yourself.

## Dashboard

For the things that need room to read or edit. Dark Tokyo Night theme, light theme follows your system.

| Tab | What it shows |
|---|---|
| Overview | on/off, the **DPI mode** (Always or Learn), counters (including connections open right now), **Test Steam connection** with the result shown inline, sharing address with phone steps |
| Devices | **Phone setup** (proxy address with a Copy button and a check that it answers, plus a QR code to identify a phone) and who uses the proxy: name, address, MAC, last seen, traffic. Click a name to rename it |
| Activity | recent connections: bypassed, direct, auto-skipped, learned bypass, failed, with one-click **Skip** (never bypass) and **Bypass** (always bypass) |
| Logs | live log with info/debug filters, search, pause, copy, clear |
| Sites | the two lists of what is known about sites, **Needs bypass** and **Never bypass**: add and remove entries (no text editor needed). Also shows where each entry came from |

Only this PC can open the dashboard: other devices get a 403, the page must be opened as `127.0.0.1` or `localhost`, and every change needs a header other websites can't send.

**Identify a phone (real model):** Chrome hides the phone model in web traffic, but a page can read it with JavaScript, and only on HTTPS. So with sharing on, the PC also serves a small HTTPS page (`https://<PC-IP>:18443/identify`, with a self-signed certificate). In the dashboard's Devices tab, scan the QR code with the phone (or copy the address). The browser warns the page is not private: tap **Advanced > Proceed**. The page reads the model and OS version (what `deviceinfo.me` shows) and sends them to the PC, and the device appears with its real name, for example `SHARK PAR-A0 · Android 12`. **Is the phone using the proxy?** After reading the model, the page checks. It requests a made-up name that exists nowhere, so the request can only reach the PC if the phone sends its HTTPS traffic through the proxy; the PC reports whether it saw it. If the phone is using the proxy the page says so. If it is not, or the check cannot be done, the page shows the proxy address with a **Copy proxy address** button (the address for the PC address the phone used).

**Why the warning, and can it be avoided?** Browsers only give the model to HTTPS pages, and nothing on the phone trusts a certificate the PC made itself, so the warning always appears the first time, with or without the proxy. The certificate is kept in `identify-cert.pem` next to the exe (renewed automatically when it is about to expire or the PC's address changes), so the browser's "proceed anyway" choice keeps matching after the app restarts. If you would rather not see the warning at all, skip this step and rename the device in the dashboard instead. This does **not** change any phone setting: the proxy address is still pasted into the phone's Wi-Fi proxy setting by hand. Safari and Firefox keep the model private, so use Chrome for this step.

**Device names:** a device is named by you first, then by what it reported on the identify page, then by its hostname if the network knows it, then by what it says it is in its User-Agent (for example `Android 14 · SM-S918B` or `iPhone14,5 · iOS 17.2`). Hover a device name to see the raw User-Agent headers it sent; each distinct header is also written once to `dpi-debug.log` (`device 192.168.1.13 says: ...`). Chrome on Android and Safari hide the exact model in web traffic, so the model only shows when the phone's system or an app sends it (the system fetch of the proxy address often does). Otherwise rename the device once. Phones behind a second router (NAT) share one address and show up as one device with several descriptions.

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

Choose in the dashboard (Overview > DPI mode). The mode only decides what happens for sites **nothing is known about yet**; it never changes what has been learned, so you can switch back and forth freely.

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

## A game or app breaks? (e.g. League of Legends)

- **Automatic:** in Always mode the app learns `NO_BYPASS` when the bypass keeps breaking a site that works normally. Check the **Sites** tab, and use Learn mode if you would rather start from a normal connection.
- **Manual:** add the domain as **Never bypass** in the dashboard's Sites tab (or one per line in `blacklist.txt`). A domain also covers its subdomains. Edits to either file apply within 2 seconds, no restart.

## Upgrading from an earlier version (data migration)

On the first start of this version the app migrates its data folder once. It is safe to run again: the result is recorded as `schema` in `settings.json` and a folder at the current version is never touched.

| Before | After |
|---|---|
| `blacklist.txt` entries (the "skip list": connect directly) | the same file, **unchanged**, now meaning `NO_BYPASS`. Nothing is rewritten, converted or deleted |
| no "needs bypass" data existed | `bypass.txt` starts **empty**: nothing is invented, it fills from real evidence or from you |
| no `dpiMode` | `dpiMode: "always"`, which is what the app did before. A mode you already chose is never changed |

- **Backup:** `blacklist.txt` is copied once to `blacklist.txt.pre-modes.bak`. An existing backup is never overwritten.
- **Ambiguous records:** entries are labelled only by their notes: `auto-skip ...` = learned by the app, `added in dashboard` = you, anything else (no note) = `existing`, origin unknown. Earlier versions pre-filled four Riot/League of Legends domains without a note; on an existing install they are kept as `NO_BYPASS` (dropping them could silently break League of Legends again) and shown as `existing` so you can remove them. A new install is not pre-filled. Lines that are not a usable domain (for example with spaces) are left in the file, reported in the log, and never match anything.
- **If the migration fails** (for example the backup cannot be written) nothing is changed and it is retried at the next start; the existing skip list keeps working meanwhile. The log line `data migrated to DPI modes: ...` (or `did not finish`) in `dpi-info.log` says what happened.
- **Roll back:** copy `blacklist.txt.pre-modes.bak` over `blacklist.txt` and delete the `schema` line in `settings.json`. An older version simply reads the unchanged `blacklist.txt` and ignores `bypass.txt` and the new settings.

## Files (next to the program)

| File | Content |
|---|---|
| `blacklist.txt` | sites that never use the bypass (`NO_BYPASS`), also editable in the dashboard |
| `bypass.txt` | sites that need the bypass (`NEEDS_BYPASS`), also editable in the dashboard |
| `blacklist.txt.pre-modes.bak` | copy of `blacklist.txt` made once when migrating from an earlier version |
| `settings.json` | saved choices, for example `{"lanSharing": true, "dpiMode": "learn", "schema": 2}` |
| `identify-cert.pem` | the identify page's self-signed certificate and key (only for that page; delete it and a new one is made) |
| `devices.json` | names you gave to devices and what phones reported on the identify page |
| `dpi-info.log` | start/stop, failures, `AUTO-SKIP`, `AUTO-BYPASS`, `NO REPLY` (recreated on every start) |
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

Layout: `cmd/dpi-bypass` (entry), `internal/policy` (modes, decisions, evidence), `internal/proxy` (split, retries, PAC), `internal/dashboard` (web UI + API), `internal/stats` (devices, identities, activity), `internal/identify` (HTTPS identify page), `internal/blacklist`, `internal/settings`, `internal/sysproxy` (Windows / macOS / Linux + crash guard), `internal/tray` (Windows icon and menu), `internal/autostart`, `internal/app`, `internal/notify`, `internal/logx`.
