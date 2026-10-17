# Technical details

[← Back to the README](../README.md)

## Vaults are rclone crypt folders

A vault is an [rclone crypt](https://rclone.org/crypt/) folder. CryptBridge uses the free tool rclone, so
you can always open your vaults with plain rclone too, even without CryptBridge: the emergency sheet
lists the commands. Existing rclone crypt vaults work as well.

CryptBridge does **not** bundle rclone or WinFsp. [rclone](https://rclone.org) (MIT License) is used as a
separate program, found on your PC or downloaded from rclone.org on request (checksum verified).
[WinFsp](https://winfsp.dev) (GPLv3 with FLOSS exception) is installed by you from its official site.

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

What the Linux setup installs and changes is listed in [Linux server](linux-server.md#what-the-setup-does).
