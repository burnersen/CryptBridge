<div align="center">

# 🔐 CryptBridge

### Your encrypted pCloud vault — as a Windows drive, and as a streaming server for Infuse.

[![License: GPLv3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
![Windows 11](https://img.shields.io/badge/Windows-11-0078D4)
![Ubuntu / Debian](https://img.shields.io/badge/Linux-Ubuntu%20%7C%20Debian-E95420)

</div>

CryptBridge keeps your files in [pCloud](https://www.pcloud.com) encrypted with
[rclone crypt](https://rclone.org/crypt/) — pCloud only ever sees scrambled names and contents — and
makes them easy to use everywhere:

- **Windows:** a small tray program shows the vault as a drive letter (`Z:` by default). Copy films in,
  play them, delete them — like any other drive.
- **Linux server:** one setup script mounts the same vault on the server and offers it over HTTPS
  (WebDAV) to **Infuse** on Apple TV, iPhone and iPad.

```
                       ┌──────────── pCloud (encrypted) ────────────┐
                       │                                            │
   Windows PC          │                    Linux server            │
   CryptBridge.exe ────┘                    rclone mount ──► ~/CryptBridge (desktop, read/write)
   Z: (read/write)                               │
                                                 └► WebDAV (read-only) ◄── Caddy (HTTPS) ◄── Infuse
```

Both sides use the **same keys**. A short **check code** (e.g. `7F3A-91C2`) is shown on both sides —
if the codes match, the keys match.

---

## ✨ What makes it safe

- **Keys are checked before anything is written.** rclone crypt never says "wrong password": with a
  wrong key a vault simply looks empty, and anything copied into it would be encrypted with the wrong
  key — the vault would be split in two. CryptBridge decrypts existing names first and refuses keys
  that do not fit.
- **No typing errors between PC and server.** The Windows program packs sign-in and keys into an
  encrypted *transfer block*; the Linux setup opens it with a one-time code.
- **Emergency sheet.** One printable page with everything needed to open the vault again — even
  without CryptBridge, with plain rclone.
- **Your keys stay yours.** On Windows the settings file is encrypted with your Windows account
  (DPAPI) and rclone keeps its configuration in memory only. On the server, keys are readable only by
  your user. Infuse gets read-only access, so a guessed or stolen Infuse password cannot delete
  anything; repeated wrong passwords are blocked by fail2ban.
- **Uploads are never cut off silently.** Copying to the drive is fast because files land in a local
  cache first; CryptBridge shows the uploads and warns before you disconnect or quit while they run.
- **The drive survives problems.** rclone runs as its own process: if the window crashes, the drive
  stays; if rclone stops, CryptBridge brings the drive back. If pCloud refuses the sign-in, CryptBridge
  says so plainly, keeps asking pCloud every minute and reconnects by itself once pCloud accepts it again.

---

## 🪟 Windows

### Requirements

- Windows 11 (64-bit)
- [WinFsp](https://winfsp.dev/rel/) — lets Windows show the vault as a drive. CryptBridge tells you if
  it is missing.
- rclone 1.75 or newer — if it is not installed, CryptBridge downloads it from rclone.org for you
  (checked against rclone's published checksum).

### Getting started

1. Install WinFsp, then start `CryptBridge.exe`. It lives in the system tray (bottom right).
2. **Vault tab → Sign in to pCloud.** Your browser opens the pCloud sign-in page. CryptBridge finds out
   by itself whether your account is in the EU or the US region. Recommended as well: request your
   [own pCloud app](#-own-pcloud-app-recommended) — it can be added at any time later.
3. **Create a new vault** (CryptBridge generates a strong password and salt — write them down or print
   the emergency sheet) **or open an existing one** ("Search pCloud" finds rclone vaults in your pCloud).
4. **Status tab → Connect.** The vault appears as drive `Z:`.

The tray icon shows the state at a glance: **green** connected · **amber** working or uploading
(do not switch the PC off) · **red** problem · **grey** disconnected.
Closing the window keeps CryptBridge running; **Quit** is in the tray menu.

### Settings

Drive letter, cache folder and size, *start with Windows*, *connect on start* and *reconnect
automatically* are on the **Settings** tab. The cache folder needs room for the largest file you copy
to the drive at once.

### 🔑 Own pCloud app (recommended)

CryptBridge signs in to pCloud through rclone. Out of the box that uses **rclone's shared pCloud
access** — which pCloud has blocked for hours at a time before: on 5 October 2026, for about three hours,
the browser showed *Invalid 'client_id'* and existing sign-ins failed with *error 2095*. With an app of
your own, CryptBridge no longer depends on it. **Your vault does not change** — same keys, same files,
same check code; you only sign in once more.

Request the app **now, while everything works**: pCloud has to approve it, and that can take a while.

1. Open **[My Apps on pCloud](https://docs.pcloud.com/my_apps/)**, log in with your pCloud account and
   request a new application.
2. Fill in the form like this and click **Create request**:

   | Field | Enter |
   |---|---|
   | App name | `CryptBridge (personal)` |
   | Application Type | **Personal use** |
   | Folder access | **All folders** — your vault is a normal folder in your pCloud; *Private* would most likely limit the app to a folder of its own |
   | Write access | **Yes** — needed to upload files |
   | Website | `https://github.com/burnersen/CryptBridge` |
   | Expected number of users | `1` |
   | Reason | the text below (also on CryptBridge's Settings tab, with a Copy button) |

   ```text
   Personal use only. I use the open-source tool CryptBridge (https://github.com/burnersen/CryptBridge), which is based on rclone, to access my own pCloud account: it shows an encrypted folder of my pCloud as a drive on my own computers. Only my own pCloud account will use this app. It needs access to all folders because my existing encrypted folders are not inside an app folder, and write access to upload my files.
   ```

3. Once pCloud has approved the app, open it on My Apps and add `http://localhost:53682/` under
   **Redirect URIs**. Without it, the sign-in ends with *redirect_uri is not authorized*.
4. In CryptBridge: **Settings → Own pCloud app**, paste the **Client ID** and the **Client secret**,
   click **Save app** and confirm the new sign-in. A connected drive is disconnected for the sign-in and
   connected again afterwards. The Vault tab then says *… through your own pCloud app*.
5. **Linux server** (not urgent — its sign-in keeps working until pCloud blocks rclone's shared access
   again): create a new transfer block, run `sudo cryptbridge setup` on the server, answer **n** to
   *Keep the pCloud sign-in and vault keys from the last setup?* and paste the block.

The codes are stored encrypted with your Windows account like the rest of the settings. rclone gets them
only for the sign-in itself, and they are not part of the transfer block: the server needs only the
sign-in.

---

## 🐧 Linux server

Tested on **Ubuntu 26.04 with an XFCE desktop** — on a server that already had its own firewall rules,
fail2ban jails and third-party package sources. Made for Ubuntu 24.04 and Debian 12 and 13 as well (not
tested there yet), with or without a desktop.

### Before you start

- A **host name** that points to your server — your own domain, or a free name from
  [DuckDNS](https://www.duckdns.org) (sign in, create e.g. `myvault.duckdns.org`, set its IP to your
  server's IP). The HTTPS certificate is fetched automatically for it.
  > ⚠️ Host names of your provider (netcup's `…supersrv.de`, Contabo's `…contaboserver.net`) often do **not**
  > work: all customers share one Let's Encrypt limit of 50 certificates per week, and it is usually used up.
  > DuckDNS names have a limit of their own.
- **Ports 80 and 443** must be reachable (also in your provider's firewall, if it has one).

### Setup

```bash
curl -fsSLO https://raw.githubusercontent.com/burnersen/CryptBridge/main/linux/setup.sh
sudo bash setup.sh
```

The setup asks a few questions and then does everything else:

| Step | What happens |
|---|---|
| Packages | rclone (checksum verified), Caddy from Ubuntu/Debian, fuse3, ufw, fail2ban, apache2-utils |
| Keys | paste the **transfer block** from the Windows program (*Vault tab → Linux server*) and type its code — or enter everything by hand |
| Check | keys checked against your vault; the **check code** must equal the one in the Windows program |
| Vault | mounted at `~/CryptBridge` for your desktop user (`rclone-mount.service`) |
| Infuse | WebDAV, read-only (`rclone-webdav.service`) behind Caddy with HTTPS; if rclone crashes, both services come back by themselves |
| Firewall | opens 80/443 — and keeps your SSH and remote-desktop ports open before switching ufw on |
| Desktop | icons *CryptBridge Vault* and *CryptBridge*, a file-manager bookmark, no film thumbnails |

Afterwards, `cryptbridge` (or the *CryptBridge* desktop icon) shows the status, the Infuse settings,
restarts the services or runs the setup again. `sudo cryptbridge uninstall` removes everything except
your files in pCloud.

### Infuse

In Infuse: **Settings → Shares → Add share → WebDAV**

| Field | Value |
|---|---|
| Protocol | WebDAV, HTTPS on |
| Address | your server's host name |
| Port | 443 |
| Path | `/` |
| User / password | the Infuse account you chose in the setup |

> 💡 **For smooth 4K playback:** in Infuse go to **Settings → Playback (Streaming) → Buffer** and set it to
> **Generous**.

### Moving to a new server

1. Run the setup on the new server (paste a fresh transfer block — or copy `/etc/cryptbridge/` over).
2. Check that the check code is the same.
3. Point your host name to the new server — with your own domain Infuse needs no change at all; with a
   provider host name, enter the new name in Infuse once.
4. Uninstall on the old server: `sudo cryptbridge uninstall`.

Your files stay in pCloud the whole time; the servers only pass them through.

---

## 📦 Moving existing (unencrypted) files into the vault

Copy them into the vault folder (on Windows the drive, on the server `~/CryptBridge`), check that they
arrived, then delete the originals. Plan for space: **until you delete an original, the film exists
twice in pCloud** (plain and encrypted). Move in portions that fit into your free pCloud space, and
check whether deleted files still count against your storage while they sit in pCloud's trash.

---

## ⚙️ Streaming settings

Used by the Windows drive and by the server mount (the server's WebDAV reads from that mount):

| rclone option | Value | Why |
|---|---|---|
| `--buffer-size` | 128M | absorbs network hiccups in memory |
| `--vfs-read-chunk-size` / `-limit` | 64M / 1G | fast start of a film, then large reads |
| `--vfs-cache-mode` | full | reads and writes go through a disk cache |
| `--vfs-cache-max-size` | 20G (server: half the free space at most) | keeps the disk from filling up |
| `--vfs-cache-min-free-space` | 10G Windows / 5G server | emergency brake for the disk |
| `--vfs-read-ahead` | 128M | reads ahead of the player |
| `--dir-cache-time` / `--poll-interval` | 24h / 1m | fast browsing; pCloud reports changes every minute |

`--no-modtime` is used for the server's WebDAV only. The drives keep real file dates: pCloud delivers
them with every folder listing, so they cost nothing, and you can sort by date.

---

## 🔧 Troubleshooting

| Problem | Answer |
|---|---|
| Check codes differ between PC and server | The keys or settings differ. Bring them over again with a transfer block. |
| "These keys do NOT open the vault" | Password, salt, folder or an advanced setting does not match. Nothing was changed. |
| "pCloud no longer accepts the sign-in (error 2095)" | Often a passing problem at pCloud: CryptBridge asks again every minute and reconnects by itself. After a password change or "log out of all devices" it lasts — click **Sign in again** on the Status tab, then bring the sign-in to the server with a new transfer block. |
| Browser shows "Invalid 'client_id'" during sign-in | pCloud is blocking rclone's shared access. Cancel and try again later, or use your [own pCloud app](#-own-pcloud-app-recommended). With your own app: check the client ID on the Settings tab. |
| Browser shows "redirect_uri is not authorized" | Add `http://localhost:53682/` to the Redirect URIs of your app on pCloud. |
| Drive letter in use | Choose another letter on the Settings tab. |
| Quit warns about uploads | Files not yet in pCloud stay on this PC and are uploaded the next time you connect. Do not delete the cache folder in the meantime. |
| Infuse cannot connect | `cryptbridge` on the server shows which service is not running; check ports 80/443 at your provider. |
| No HTTPS certificate, log says `rateLimited` | Your provider's host name shares Let's Encrypt's weekly limit with all customers. Use a DuckDNS name or your own domain and run `sudo cryptbridge setup` again (keep keys and Infuse account). |

Logs: Windows `%LOCALAPPDATA%\CryptBridge\logs\rclone.log` · server `journalctl -u rclone-mount -u rclone-webdav -u caddy`

---

## 🛠️ Building from source

Windows program (Go 1.27 as set in `windows/go.mod`, PowerShell 7):

```powershell
pwsh -File windows\build.ps1
```

The version lives in one place, the file `VERSION`; the build script writes it into the program, its file
properties and `linux/setup.sh`. Tests: `go test ./...` in `windows/`, `bash linux/tests/functions_test.sh`.

---

## 📜 License

CryptBridge is free software under the [GNU General Public License, version 3](LICENSE) — that version only (`GPL-3.0-only`).

- **Use it:** Anyone may use CryptBridge free of charge, at home or at work, and read, change and share its source code.
- **The one condition:** Whoever passes on CryptBridge or parts of it — unchanged, changed or built into a program of their own — must include the complete source code and pass everything on under the same license. A closed, paid product with CryptBridge inside is therefore not possible.
- **Free of charge:** CryptBridge is and stays free. The official source is [github.com/burnersen/CryptBridge](https://github.com/burnersen/CryptBridge) — anyone asking money for CryptBridge is selling you what you can get here for free.
- **A separate license for companies:** Want to build CryptBridge into a product that is not meant to be under the GPLv3? You can ask the author for a separate license — just [open an issue](https://github.com/burnersen/CryptBridge/issues).

There is **no warranty**. Keep your emergency sheet: without the password and the salt, nobody can
open the vault.

### rclone and WinFsp

CryptBridge does **not** bundle rclone or WinFsp. [rclone](https://rclone.org) (MIT License) is used as a
separate program — found on your PC or downloaded from rclone.org on request. [WinFsp](https://winfsp.dev)
(GPLv3 with FLOSS exception) is installed by you from its official site.

### Third-party components

The Windows program is built with these Go libraries. Each keeps its own license, and all of them are
compatible with the GPLv3:

- **MIT License:** [Wails](https://github.com/wailsapp/wails) with [go-webview2](https://github.com/wailsapp/go-webview2) and [mimetype](https://github.com/wailsapp/mimetype), [go-toast](https://git.sr.ht/~jackmordaunt/go-toast), [bep/debounce](https://github.com/bep/debounce), [go-ole](https://github.com/go-ole/go-ole), [leaanthony/go-ansi-parser](https://github.com/leaanthony/go-ansi-parser), [leaanthony/slicer](https://github.com/leaanthony/slicer), [leaanthony/u](https://github.com/leaanthony/u), [rivo/uniseg](https://github.com/rivo/uniseg), [samber/lo](https://github.com/samber/lo)
- **Apache License 2.0:** [fyne.io/systray](https://github.com/fyne-io/systray), [tkrajina/go-reflector](https://github.com/tkrajina/go-reflector)
- **BSD 2-Clause License:** [godbus/dbus](https://github.com/godbus/dbus), [pkg/browser](https://github.com/pkg/browser), [pkg/errors](https://github.com/pkg/errors)
- **BSD 3-Clause License:** the [Go standard library](https://go.dev/LICENSE), [golang.org/x/net](https://github.com/golang/net), [x/sys](https://github.com/golang/sys), [x/text](https://github.com/golang/text), [google/uuid](https://github.com/google/uuid)

Their copyright notices and full license texts are in the repositories linked above.
