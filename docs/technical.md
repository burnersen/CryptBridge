# Technical details

[← Back to the README](../README.md)

## Vaults are rclone crypt folders

A vault is an [rclone crypt](https://rclone.org/crypt/) folder. CryptBridge uses the free tool rclone, so
you can always open your vaults with plain rclone too, even without CryptBridge: the emergency sheet
lists the commands. Existing rclone crypt vaults work as well.

CryptBridge does **not** build rclone or WinFsp into the program. [rclone](https://rclone.org)
(MIT License) runs as a separate program. The Windows program and the Linux setup use exactly the tested
rclone 1.75.2 and check every download against a checksum built into CryptBridge: from rclone.org first,
then from rclone's release on GitHub, then from the copy kept with the CryptBridge release. After a
CryptBridge update, the matching rclone is downloaded automatically. [WinFsp](https://winfsp.dev)
(GPLv3 with FLOSS exception) is installed by you from its official site. WinFsp - Windows File System
Proxy, Copyright (C) Bill Zissimopoulos, [github.com/winfsp/winfsp](https://github.com/winfsp/winfsp).

Each [release](https://github.com/burnersen/CryptBridge/releases) also keeps unchanged copies of rclone's
original ZIP files and of the WinFsp installer as a backup, together with both license texts. The
official download pages come first; install the WinFsp copy only if WinFsp is not installed yet.

## Streaming settings (rclone options)

Used by the Windows drive and by the server mount (the server's WebDAV reads from that mount):

| rclone option | Value | Why |
|---|---|---|
| `--buffer-size` | 128M | absorbs network hiccups in memory |
| `--vfs-read-chunk-size` / `-limit` | 64M / 1G | fast start of a film, then large reads |
| `--vfs-cache-mode` | full | reads and writes go through a disk cache |
| `--vfs-cache-max-size` | 20G (server: half the free space at most) | keeps the disk from filling up |
| `--vfs-cache-min-free-space` | 10G Windows / 5G server | emergency brake for the disk |
| `--vfs-read-ahead` | 128M | reads ahead of the player |
| `--dir-cache-time` / `--poll-interval` | 24h / 1m | fast browsing; the cloud reports changes every minute |

`--no-modtime` is used for the server's WebDAV only. The drives keep real file dates: the clouds deliver
them with every folder listing, so they cost nothing, and you can sort by date.

Dropbox accounts get `batch_mode off`. With rclone's default batch uploads, a key check that rclone drops
from its cache ends the upload batcher of the same Dropbox folder, and every later upload fails with
*batcher is shutting down*.

Vaults on the PC do not need any of the streaming values: they use `--vfs-cache-mode writes`,
`--vfs-cache-max-age 1m` and `--dir-cache-time 10s`.

## Building from source

Windows program (Go 1.27 as set in `windows/go.mod`, PowerShell 7):

```powershell
pwsh -File windows\build.ps1
```

The version lives in one place, the file `VERSION`; the build script writes it into the program, its file
properties and `linux/setup.sh`. Tests: `go test ./...` in `windows/`, `bash linux/tests/functions_test.sh`.

Releases are built by GitHub with `.github/workflows/release.yml`: push a tag such as `v0.12.0` (equal
to `VERSION`), or start it on `main` under *Actions > Release > Run workflow*, which makes the tag from
`VERSION`. On a fresh Windows machine it collects the backup copies
(`windows/release-assets.ps1`), runs the tests with exactly the pinned rclone, builds `CryptBridge.exe`
(`windows/build.ps1`) and publishes the release. Its text is the backup part followed by
`.github/release-notes/<version>.md`, which has to be in the tagged commit. If any step fails, nothing
is published. Ticking *dry run* (any branch) runs all of it without signing, tagging or publishing.

Code signing through the SignPath Foundation is prepared in the same workflow and stays off until it is
set up: [`.signpath/README.md`](../.signpath/README.md). Only `CryptBridge.exe` is ever signed; the
policy is [CODE_SIGNING.md](../CODE_SIGNING.md).

What the Linux setup installs and changes is listed in [Linux server](linux-server.md#what-the-setup-does).
