<div align="center">

# 🔐 CryptBridge

### Your encrypted vaults — in pCloud or on your own disks — as Windows drives, and as a streaming server for Infuse.

[![License: GPLv3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
![Windows 11](https://img.shields.io/badge/Windows-11-0078D4)
![Ubuntu / Debian](https://img.shields.io/badge/Linux-Ubuntu%20%7C%20Debian-E95420)
[![Ko-fi](https://img.shields.io/badge/Ko--fi-Support%20me-FF5E5B?logo=kofi&logoColor=white)](https://ko-fi.com/burnersen)

**[⬇️ Download the latest release](https://github.com/burnersen/CryptBridge/releases/latest)** · **[☕ Buy me a coffee](https://ko-fi.com/burnersen)**

</div>

CryptBridge keeps your files in [pCloud](https://www.pcloud.com) encrypted with
[rclone crypt](https://rclone.org/crypt/) — pCloud only ever sees scrambled names and contents — and
makes them easy to use everywhere:

- **Windows:** a small tray program shows each vault as a drive letter (`Z:` by default). Copy films in,
  play them, delete them — like any other drive. Several vaults can be open at once, each with its own
  letter: vaults in pCloud, and [vaults in a folder of your PC or on a USB drive](#-vaults-on-this-pc-or-a-usb-drive)
  that are encrypted and decrypted only on your PC — no cloud and no sign-in needed.
- **Home network (optional):** the Windows program can also share the connected drives with the other
  devices of your home network over WebDAV — Infuse, VLC or Kodi on a TV, phones, tablets, a Mac. With a
  VPN into your home network (for example the WireGuard VPN of your router) your PC at home serves them
  [away from home, too](#away-from-home-vpn-into-your-home-network) — without renting a server. See
  [LAN access](#-lan-access-optional).
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
- **No vault is overwritten by mistake.** A new vault is only created in an empty folder, never inside
  another vault, and two vaults can never share a folder. A vault on a USB drive connects only to the
  very folder it was connected to before — not to a copy on another drive with the same letter — and
  its drive is closed at once when the folder disappears. Removing a vault from CryptBridge never
  touches its files.
- **No typing errors between PC and server.** The Windows program packs sign-in and keys into an
  encrypted *transfer block*; the Linux setup opens it with a one-time code.
- **Emergency sheet.** One printable page with everything needed to open the vault again — even
  without CryptBridge, with plain rclone.
- **Your keys stay yours.** On Windows the settings file is encrypted with your Windows account
  (DPAPI) and rclone keeps its configuration in memory only. With the optional
  [app lock](#-app-lock-recommended), sign-in and keys are also sealed with a password of your own, and
  CryptBridge locks itself together with Windows. On the server, keys are readable only by
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
2. For vaults in pCloud: **Vaults tab → pCloud account → Sign in to pCloud.** Your browser opens the
   pCloud sign-in page. CryptBridge finds out by itself whether your account is in the EU or the US
   region. Recommended as well: request your [own pCloud app](#-own-pcloud-app-recommended) — it can be
   added at any time later. Vaults on your PC need no sign-in.
3. **Vaults tab → Add vault**, then choose where it is — **in pCloud** or **on this PC or a USB drive** —
   and whether to **create a new vault** (CryptBridge generates a strong password and salt — write them
   down or print the emergency sheet) **or open an existing one**. *Browse* shows your pCloud folders
   (vaults are marked with a lock) or the folders of your PC; *Search pCloud* finds rclone vaults in
   your whole pCloud. Give the vault a name and a drive letter.
4. The vault is connected right away and appears as its drive. The **Status tab** shows all drives with
   their state, the free space where they live, their size and running uploads.

Every vault has its own page on the **Vaults tab** — the list on the left shows which one you are on:
its details and check code, name, drive letter, *connect on start*, the emergency sheet, the transfer to
the Linux server (pCloud vaults) and *Remove from CryptBridge* (asks for the vault's name; the files of
the vault stay untouched).

The tray icon shows the state at a glance: **green** connected · **amber** working or uploading
(do not switch the PC off) · **red** a drive has a problem · **grey** disconnected or locked. Its menu
connects or disconnects all drives, or each one on its own. Closing the window keeps CryptBridge
running; **Quit** is in the tray menu.

### Settings

On the **Settings** tab: light or dark look (or *automatic*, like Windows), the cache folder and size
(shared by all drives), *start with Windows*, *reconnect automatically*, the app lock and the LAN access. The cache
folder needs room for the largest file you copy to a drive at once. Name, drive letter and *connect on
start* are set for each vault on its page.

### 💾 Vaults on this PC or a USB drive

A vault can also be a folder on any disk of your PC — an internal disk, a USB stick or an external
disk, or a network share connected as a drive letter. It is encrypted and decrypted only on your PC with
the same rclone crypt as in pCloud, and needs neither pCloud nor a sign-in. The folder itself only ever
holds encrypted files: you can copy it to another disk as a backup, and it stays encrypted.

- **USB drives:** with *connect on start*, the vault connects as soon as its drive is plugged in (and
  CryptBridge is running). If the drive is pulled out while the vault is connected, CryptBridge closes
  the vault's drive within about two seconds — please disconnect first anyway, as with any USB drive.
  Files that were still being written stay on the PC and are written into the vault when it is back.
- **Copies are not mixed up:** if a copy of the vault (for example a backup on a second stick) shows
  up under the same path, CryptBridge does not connect it by itself but asks first — otherwise your
  changes would end up in the wrong copy. The same happens if you move the vault to a new disk: confirm
  once with *This is my vault — use this folder*.
- **The check code** of a local vault does not depend on the drive letter its disk gets.
- **The cache:** a local disk is fast, so only files being written pass through the cache, and they
  leave it a minute after their last use. The cache holds them decrypted — turn on BitLocker for the
  system disk, as for pCloud vaults.
- Not possible on a Windows disk: vaults with *base64* file names, because Windows does not tell upper
  from lower case (two encrypted names could land on one file).

### Updating from version 0.3

CryptBridge converts its settings by itself: your vault becomes the first vault in the list, called
*Vault*, on the same drive letter and with the same keys (rename it on its page if you like). With the
app lock on, this happens at the first unlock. The old settings file is kept once as
`%APPDATA%\CryptBridge\settings.dat.v1-backup`; it is deleted when you set, change or reset the app
password. Version 0.3 cannot read the new settings — if you ever have to go back, rename the backup to
`settings.dat`.

### 🔒 App lock (recommended)

Without it, anyone who uses your Windows account — or sits at your unlocked PC — can open the drives and
the emergency sheets: Windows decrypts CryptBridge's settings for everyone signed in to that account.
With an **app password** (Settings → *App lock*; CryptBridge offers it once after setup):

- CryptBridge **starts locked** and asks for the app password once after Windows starts. If a vault
  connects on start, the window with the password field appears by itself.
- It **locks itself again with Windows** — `Win+L`, the automatic screen lock, signing out — and asks
  again when you are back (if drives were connected); they connect again after unlocking.
- **Locked means nothing readable is left behind:** all drives are closed (running uploads are finished
  first; when you sign out there is no time for that — waiting files stay in the cache and are uploaded
  after the next unlock), rclone is stopped, the cache with readable copies of recently used files and
  rclone's log are deleted, and sign-in, vault folders and keys exist only sealed with the app password
  (Argon2id + AES-256, inside the Windows encryption).
- The **emergency sheets** and the **transfer block** ask for the app password again.
- Wrong passwords make the next try wait longer (up to 30 seconds).

**Forgot the app password?** It cannot be recovered, but your files are not lost: *Forgot the app
password? → Reset CryptBridge* on the lock screen, then sign in to pCloud again and add your vaults again
with password and salt from their **emergency sheets**.

What the app lock cannot cover: Windows itself remembers recently opened file names and preview
pictures, and deleted files can be recovered from an unencrypted disk. Turn on **BitLocker** for the
disk with the cache folder, and lock Windows when you leave the PC.

### 🔑 Own pCloud app (recommended)

CryptBridge signs in to pCloud through rclone. Out of the box that uses **rclone's shared pCloud
access** — which pCloud has blocked for hours at a time before: on 5 October 2026, for about three hours,
the browser showed *Invalid 'client_id'* and existing sign-ins failed with *error 2095*. With an app of
your own, CryptBridge no longer depends on it. **Your vaults do not change** — same keys, same files,
same check codes; you only sign in once more.

> **Status (October 2026):** CryptBridge is fully usable right away with rclone's shared access — the own
> app is optional. The own-app sign-in is built in and tested up to pCloud's sign-in page, but a complete
> sign-in with an approved app has not been tested yet: the approval of our own test app is still pending
> at pCloud. If you try it first, feedback in the issues is welcome.

Request the app **now, while everything works**: pCloud has to approve it, and that can take a while.

1. Open **[My Apps on pCloud](https://docs.pcloud.com/my_apps/)**, log in with your pCloud account and
   request a new application.
2. Fill in the form like this and click **Create request**:

   | Field | Enter |
   |---|---|
   | App name | `CryptBridge (personal)` |
   | Application Type | **Personal use** |
   | Folder access | **All folders** — your vaults are normal folders in your pCloud; *Private* would most likely limit the app to a folder of its own |
   | Write access | **Yes** — needed to upload files |
   | Website | `https://github.com/burnersen/CryptBridge` |
   | Expected number of users | `1` |
   | Reason | the text below (also in CryptBridge at *Vaults → pCloud account*, with a Copy button) |

   ```text
   Personal use only. I use the open-source tool CryptBridge (https://github.com/burnersen/CryptBridge), which is based on rclone, to access my own pCloud account: it shows an encrypted folder of my pCloud as a drive on my own computers. Only my own pCloud account will use this app. It needs access to all folders because my existing encrypted folders are not inside an app folder, and write access to upload my files.
   ```

3. Once pCloud has approved the app, open it on My Apps and add `http://localhost:53682/` under
   **Redirect URIs**. Without it, the sign-in ends with *redirect_uri is not authorized*.
4. In CryptBridge: **Vaults → pCloud account → Own pCloud app**, paste the **Client ID** and the
   **Client secret**, click **Save app** and confirm the new sign-in. Connected pCloud drives are
   disconnected for the sign-in and connected again afterwards. The page then says *… through your own
   pCloud app*.
5. **Linux server** (not urgent — its sign-in keeps working until pCloud blocks rclone's shared access
   again): create a new transfer block on the page of the vault, run `sudo cryptbridge setup` on the
   server, answer **n** to *Keep the pCloud sign-in and vault keys from the last setup?* and paste the
   block.

The codes are stored encrypted with your Windows account like the rest of the settings. rclone gets them
only for the sign-in itself, and they are not part of the transfer block: the server needs only the
sign-in.

### 🏠 LAN access (optional)

CryptBridge can share the connected drives with the other devices of your home network — a TV or Apple TV
with Infuse, VLC or Kodi, a phone, a tablet, a Mac. Turn it on at **Settings → LAN access**: every
connected drive then appears there as a folder, under an address such as `http://192.168.1.20:8080/`
(WebDAV, with user name and password; CryptBridge suggests a password that is easy to type with a TV
remote).

- **It follows the drives.** A drive that connects appears in the network; a drive leaves the network
  *before* it disconnects. **Locking CryptBridge ends the LAN access at once**, even while drives still
  finish their uploads. The PC has to be on and awake.
- **Read only by default.** The devices can open, play and copy files, but cannot change or delete
  anything. *Allow changes from the network* lets them upload, rename and delete; such files go into the
  drive's upload queue like files copied in Explorer.
- **Only your home network.** When you turn it on, CryptBridge asks Windows (with the usual
  administrator prompt) for one firewall rule: inbound connections to the chosen port, for rclone only,
  from devices of the same network, and only on networks Windows counts as **private**. Turning the LAN
  access off removes the rule again (Windows asks once more). If Windows counts your home network as
  *public*, CryptBridge says so — set it to private in Windows Settings → Network & internet → your
  connection → *Network profile type*.
- **Mind what it means:** in your home network the files travel **decrypted** and unencrypted (plain
  HTTP), protected by the password only. Do not turn it on in networks you do not trust.
- **In Infuse** (or VLC, Kodi, a file app): add a WebDAV share with the address, port, user name and
  password from the Settings page (protocol `http`, path `/`). On a Mac: Finder → Go → Connect to Server.
- **Other Windows PCs** refuse password-protected `http://` WebDAV addresses unless their registry is
  changed — install CryptBridge there instead.

#### Away from home: VPN into your home network

The LAN access is meant for your home network only — never open its port to the internet on your
router. To use it on the road, connect your phone, tablet or laptop to your home network through a
**VPN**: the device then reaches the LAN access as if it were at home, the VPN encrypts the whole way, and
your own PC at home does the job of a rented server. How smoothly films play then depends on the upload
speed of your internet connection at home.

CryptBridge's firewall rule lets in devices that have an address of your home network. So pick a VPN
that gives them one:

- **The WireGuard VPN of your router** — for example a FRITZ!Box: devices connected through it get an
  address of your home network and are let in like at home. Nothing to change in CryptBridge.
- **Tailscale with a subnet router** in your home network (a router, NAS or Raspberry Pi that advertises
  your home network): it passes the connections on with its own home network address, which Tailscale
  does by default, so they are let in, too. Tailscale running only on this PC is *not* supported yet: its
  devices have addresses of their own (100.x.x.x), which the rule does not let in.

*Not tested yet* — reports are welcome.

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
| Keys | paste the **transfer block** from the Windows program (*Vaults tab → the page of the vault → Linux server*; pCloud vaults only) and type its code — or enter everything by hand |
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

Vaults on the PC do not need any of the streaming values: they use `--vfs-cache-mode writes`,
`--vfs-cache-max-age 1m` and `--dir-cache-time 10s`.

---

## 🔧 Troubleshooting

| Problem | Answer |
|---|---|
| Check codes differ between PC and server | The keys or settings differ. Bring them over again with a transfer block. |
| "These keys do NOT open the vault" | Password, salt, folder or an advanced setting does not match. Nothing was changed. |
| "pCloud no longer accepts the sign-in (error 2095)" | Often a passing problem at pCloud: CryptBridge asks again every minute and reconnects by itself. After a password change or "log out of all devices" it lasts — click **Sign in again** on the Status tab, then bring the sign-in to the server with a new transfer block. |
| Browser shows "Invalid 'client_id'" during sign-in | pCloud is blocking rclone's shared access. Cancel and try again later, or use your [own pCloud app](#-own-pcloud-app-recommended). With your own app: check the client ID at *Vaults → pCloud account*. |
| Browser shows "redirect_uri is not authorized" | Add `http://localhost:53682/` to the Redirect URIs of your app on pCloud. |
| Drive letter in use | Choose another letter on the page of the vault (while it is disconnected). |
| Quit warns about uploads | Files not yet in the vault stay on this PC and are written into it the next time you connect. Do not delete the cache folder in the meantime. |
| A vault on a USB drive says "Waiting for its folder" | Its drive is not plugged in (or got another letter). It connects by itself when the folder is back. If the drive now has another letter, add the vault again from there and remove the old entry. |
| "… is not the folder the drive was last connected to" | Another drive with a copy of the vault, or the vault was moved. If it is the vault you want to work with from now on, click *This is my vault — use this folder*. |
| "The keys … do not open the folder" (local vault) | Another drive got the vault's drive letter. Nothing was connected or written. |
| Remove refuses: files still wait | Connect the vault and let the uploads finish; removing it now would lose those files. |
| Infuse cannot connect | `cryptbridge` on the server shows which service is not running; check ports 80/443 at your provider. |
| A device in the home network cannot reach the LAN access | *Settings → LAN access* shows the firewall state: allow it there if needed, and set your network to *private* in Windows if CryptBridge says it is public. Use the address shown there; the PC must be awake. |
| LAN access: "port … is used by another program" | Choose another port (1024–65535) and save; the firewall rule is updated with it. |
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
- **BSD 3-Clause License:** the [Go standard library](https://go.dev/LICENSE), [golang.org/x/crypto](https://github.com/golang/crypto), [x/net](https://github.com/golang/net), [x/sys](https://github.com/golang/sys), [x/text](https://github.com/golang/text), [google/uuid](https://github.com/google/uuid)

Their copyright notices and full license texts are in the repositories linked above.

---

## ☕ Support

CryptBridge is free and made in my spare time, on my own hardware and electricity bill. If it keeps your
files safe and your films playing and you'd like to say thanks, you can
[buy me a coffee on Ko-fi](https://ko-fi.com/burnersen). Completely optional — and either way: thank you
for using it!
