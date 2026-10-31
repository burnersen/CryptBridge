# Code signing policy

> **Status:** `CryptBridge.exe` is not code signed yet. Free code signing
> through the [SignPath Foundation](https://signpath.org) is planned as soon as
> CryptBridge is known well enough to qualify. Everything for it is prepared, and
> this policy already describes how it will work. Every release says in its
> text whether its files are signed.
>
> Once signing is active: free code signing provided by
> [SignPath.io](https://about.signpath.io), certificate by
> [SignPath Foundation](https://signpath.org).

## What is signed

Only `CryptBridge.exe`, built from the source code in this repository by the
[release workflow](.github/workflows/release.yml) on GitHub-hosted runners.
Nothing that was built elsewhere is signed with this certificate. Every
signing request is approved by hand, release by release.

Never signed by this project: the copies of the **rclone** ZIP files and of
the **WinFsp** installer that each release keeps as a backup. They are the
unchanged originals of their projects, checked against their known SHA-256
([`windows/release-assets.ps1`](windows/release-assets.ps1)). `setup.sh`
(Linux server) is a shell script and is not signed either.

The file properties of the signed exe name the product `CryptBridge` and the
version of the release; SignPath refuses to sign a file that does not match.

## Team roles

| Role | Who |
| ---- | --- |
| Committers and reviewers | [burnersen](https://github.com/burnersen) |
| Approvers | [burnersen](https://github.com/burnersen) |

Changes from anybody else (pull requests) are reviewed by a committer before
they are merged. All team members use multi-factor authentication for GitHub
and for SignPath.

## Privacy policy

This program will not transfer any information to other networked systems
unless specifically requested by the user or the person installing or
operating it.

What CryptBridge does on the network, and only for that:

- **Your cloud accounts:** CryptBridge connects only to the cloud services
  you add yourself (pCloud, Google Drive, OneDrive, Dropbox, MEGA), with your
  own sign-in. Files are **encrypted on your PC before** they are uploaded;
  the service only ever gets encrypted data. For the storage overview it asks
  the service how much space is used.
- **rclone download:** only if the tested rclone is missing (first start, or
  after a CryptBridge update that needs another rclone), CryptBridge downloads
  it from rclone's own servers (downloads.rclone.org, GitHub), or from the
  backup copy in this project's releases, and checks it against a checksum
  built into CryptBridge.
- **LAN access** (off unless you turn it on): shares the connected drives
  with devices in your home network over WebDAV, protected by user name and
  password. See [LAN access](docs/lan-access.md).

Like any network request, these reveal your IP address to the server asked.
Your vault passwords and keys are never sent anywhere: they leave your PC
only if you pass them on yourself (the emergency sheet, or the encrypted
transfer block you copy to your own Linux server). There is no telemetry, no
update check, no account with this project and no advertising.

## Changes to your system

CryptBridge tells you before it changes anything outside its own folders,
and only does so when you ask for it:

- **Start with Windows** (Settings tab): an entry in your user's
  `Run` key, removed again when you turn it off.
- **LAN access:** one Windows firewall rule `CryptBridge LAN access (WebDAV)`,
  added with your permission (Windows asks) and removed when you turn both
  accesses off.
- **WinFsp** is installed by you, from its official site.

## Installing and uninstalling

There is no installer: CryptBridge is one exe. To uninstall:

1. Let all uploads finish, then turn off *start with Windows* and the LAN
   access (this removes the firewall rule) on the Settings tab.
2. *Quit* CryptBridge in the tray menu and delete `CryptBridge.exe`.
3. Delete the folders `%APPDATA%\CryptBridge` (settings) and
   `%LOCALAPPDATA%\CryptBridge` (logs, cache and the downloaded rclone).
4. If no other program needs it, uninstall WinFsp in *Settings > Apps*.

Your encrypted files stay in your cloud. Keep the emergency sheet: without
the password and the salt nobody can open them again. The Linux server is
removed with `sudo cryptbridge uninstall`, see
[Linux server](docs/linux-server.md).

## Verifying a download

- **Signature:** right-click `CryptBridge.exe` > *Properties* > *Digital
  Signatures*. A signed release shows *SignPath Foundation*.
- **Checksums:** every release run lists the SHA-256 of each file in its log.

## Reporting a problem

If you think a signed file of this project does something it should not,
please open an [issue](https://github.com/burnersen/CryptBridge/issues).
Signed files that break these rules can also be reported to the
[SignPath Foundation](https://signpath.org).
