# The Windows program

[← Back to the README](../README.md)

Everything about CryptBridge on Windows: getting started, the tray icon, your vaults and settings, the
cache, vaults on your own disks, the app lock and moving files you already have into a vault.

- [What you need](#what-you-need)
- [Getting started](#getting-started)
- [The tray icon](#the-tray-icon)
- [Your vaults and settings](#your-vaults-and-settings)
- [The cache](#the-cache)
- [Vaults on this PC or a USB drive](#vaults-on-this-pc-or-a-usb-drive)
- [App lock (recommended)](#app-lock-recommended)
- [Moving your existing files into a vault](#moving-your-existing-files-into-a-vault)
- [Updating](#updating)

---

## What you need

- Windows 11 (64-bit).
- [WinFsp](https://winfsp.dev/rel/), a free helper that lets Windows show a vault as a drive.
  CryptBridge tells you if it is missing. WinFsp - Windows File System Proxy, Copyright (C) Bill
  Zissimopoulos, [github.com/winfsp/winfsp](https://github.com/winfsp/winfsp). Each
  [CryptBridge release](https://github.com/burnersen/CryptBridge/releases) also keeps an unchanged copy of
  the WinFsp installer as a backup. The official page comes first; install the copy only if WinFsp is not
  installed yet.
- rclone 1.75.2, the tool that does the locking. CryptBridge uses exactly this tested version. If it is
  missing, CryptBridge downloads it for you from rclone.org (or from a copy of the same file on GitHub)
  and checks it against a checksum built into CryptBridge. After a CryptBridge update, the matching rclone
  is downloaded automatically.
- For vaults in the cloud: an account at pCloud, Google Drive, OneDrive, Dropbox or MEGA. Google Drive also
  needs [your own free Google app](google-drive-app.md).

## Getting started

1. **Install WinFsp**, then download and start **`CryptBridge.exe`** from the
   [latest release](https://github.com/burnersen/CryptBridge/releases/latest). There is no installer.
   CryptBridge lives in the system tray, the small icons at the bottom right next to the clock.
2. **Add your cloud account** (only for vaults in the cloud): *Vaults tab > Add cloud account*, choose
   the service and click *Sign in*. Your browser opens the sign-in page of the service; sign in there and
   allow the access. For MEGA, type your e-mail address and password right in CryptBridge instead. For
   Google Drive, set up your own Google app first: the guide is right there on the page. See [Cloud accounts](cloud-accounts.md) for what each service needs.
3. **Add a vault:** *Vaults tab > Add vault*. Choose where it is (**in one of your cloud accounts** or
   **on this PC or a USB drive**), then:
   - **Create a new vault:** CryptBridge makes up a strong password and salt for you.
   - **Open an existing vault:** enter its password and salt. *Browse* shows your folders (vaults are
     marked with a lock); *Search* finds vaults anywhere in that cloud.

   Give the vault a name and a drive letter.
4. **Done.** The vault appears as a drive right away. The **Status tab** shows all drives, how much
   space is left where they live, their size and running uploads.

> [!WARNING]
> **Write down the password and the salt, or print the emergency sheet.** Without them nobody can
> open your vault: not your cloud provider, not CryptBridge, not me. There is no "forgot password" for a
> vault.

## The tray icon

| Colour | Meaning |
|---|---|
| **Green** | connected |
| **Amber** | working or uploading: please do not switch the PC off now |
| **Red** | a drive has a problem |
| **Grey** | disconnected, or CryptBridge is locked |

Its menu connects or disconnects all drives, or each one on its own. Closing the window keeps
CryptBridge running; **Quit** is in the tray menu.

## Your vaults and settings

Every vault has its own page on the **Vaults tab**. There you find its details and its check code, can
change its name, drive letter and *connect on start*, print its **emergency sheet**, bring it to a
[Linux server](linux-server.md) (vaults in the cloud), or *Remove from CryptBridge*. Removing asks for the
vault's name to be sure, and never touches the files in the vault.

**The emergency sheet** is one page with everything needed to open the vault again: the place of the
vault, the password, the salt, the advanced settings and the check code. It also lists the plain
[rclone](https://rclone.org) commands that open the vault **without CryptBridge**. Keep it like a house
key: anyone who has it, and access to the vault's folder, can read your vault.

On the **Settings tab**: light or dark look (or *automatic*, like Windows), *start with Windows*,
*reconnect automatically*, the [app lock](#app-lock-recommended), the [LAN access](lan-access.md)
and the **cache**.

## The cache

The cache is a folder on your PC where files wait for a short time. Copying to a drive is fast
because files land there first and are then uploaded in the background. It is shared by all drives and
needs room for the largest file you copy at once.

> [!IMPORTANT]
> The cache holds **readable copies** of your files. Turn on **BitLocker** (Windows' own disk
> encryption) for the disk with the cache folder.

---

## Vaults on this PC or a USB drive

A vault does not have to be in the cloud. It can also be a folder on any disk of your PC: an internal
disk, a USB stick, an external disk, or a network folder that has a drive letter. It is locked and
unlocked only on your PC, the same way as in the cloud, and needs neither a cloud nor a sign-in. The
folder itself only ever holds locked files, so you can copy it to another disk as a backup and it stays
locked.

- **USB drives:** with *connect on start*, the vault connects as soon as its drive is plugged in (while
  CryptBridge is running). If the drive is pulled out while the vault is connected, CryptBridge closes
  the vault's drive within about two seconds. Please disconnect first anyway, as with any USB drive.
  Files that were still being written stay on the PC and are written into the vault when it is back.
- **Copies are not mixed up:** if a copy of the vault (for example a backup on a second stick) shows up
  in the same place, CryptBridge does not connect it by itself but asks first. Otherwise your changes
  would end up in the wrong copy. The same happens if you move the vault to a new disk: confirm once with
  the button *This is my vault*.
- **The check code** of a vault on your PC does not depend on the drive letter its disk gets.
- **The cache:** a local disk is fast, so only files being written pass through the cache, and they
  leave it a minute after their last use.
- Not possible on a Windows disk: vaults whose file names use the *base64* setting. Windows does not
  tell upper from lower case in file names, so two locked names could land on the same file. The same
  goes for OneDrive and Dropbox.

---

## App lock (recommended)

Without an app lock, anyone who uses your Windows account, or sits at your PC while it is unlocked, can
open your drives and print your emergency sheets. Set an **app password** at *Settings > App lock*
(CryptBridge also offers it once after setup). Then:

- CryptBridge **starts locked** and asks for the app password once after Windows starts. If a vault
  connects on start, the password window appears by itself.
- It **locks itself together with Windows** (`Win+L`, the automatic screen lock, signing out) and asks
  again when you are back. Your drives connect again after unlocking.
- **Locked means nothing readable is left behind.** All drives are closed, the cache with readable copies
  of recently used files is deleted, and sign-in, vault folders and keys are kept only sealed with your
  app password (Argon2id and AES-256, on top of the Windows encryption). Running uploads are finished
  first. When you sign out of Windows there is no time for that: waiting files stay in the cache and are
  uploaded after the next unlock.
- **Emergency sheets** and **transfer blocks** ask for the app password again.
- After a wrong password, the next try has to wait a little longer (up to 30 seconds).

**Forgot the app password?** It cannot be recovered, but your files are not lost. Click *Forgot the app
password?* on the lock screen, then *Reset CryptBridge*. Add your cloud accounts again, then your vaults
with the password and salt from their **emergency sheets**.

> [!NOTE]
> **What the app lock cannot do:** Windows itself remembers recently opened file names and preview
> pictures, and deleted files can be recovered from an unencrypted disk. Turn on **BitLocker** for the
> disk with the cache folder, and lock Windows when you leave the PC.

---

## Moving your existing files into a vault

Copy them onto the vault's drive (on the server: into the vault's folder in `~/CryptBridge`), check that
they arrived, then delete the originals.

> [!TIP]
> Plan for space: **until you delete an original, the file is in your cloud twice** (unlocked and
> locked). Move in portions that fit into your free space in the cloud, and check whether deleted files
> still count against your storage while they sit in the cloud's trash.

## Updating

Replace `CryptBridge.exe` with the one from the
[latest release](https://github.com/burnersen/CryptBridge/releases/latest) and start it. Your settings and
vaults stay as they are. If the new version needs another rclone, it downloads it by itself and shows
the progress. Did you use an rclone of your own (on the PATH or next to `CryptBridge.exe`)? Then click
*Download rclone* once; if you use the [LAN access](lan-access.md), allow it in the firewall once more.
The [release notes](https://github.com/burnersen/CryptBridge/releases) say what
changed and whether anything else is needed. Coming from CryptBridge 0.6 or older, see
[Cloud accounts](cloud-accounts.md#updating-from-cryptbridge-06-or-older).
