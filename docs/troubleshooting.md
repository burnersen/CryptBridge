# Troubleshooting

[← Back to the README](../README.md)

- [Keys and vaults](#keys-and-vaults)
- [Signing in to a cloud](#signing-in-to-a-cloud)
- [Drives and uploads](#drives-and-uploads)
- [Vaults on this PC or a USB drive](#vaults-on-this-pc-or-a-usb-drive)
- [LAN access](#lan-access)
- [Linux server](#linux-server)
- [Log files](#log-files)

## Keys and vaults

| Problem | What to do |
|---|---|
| "These keys do NOT open the vault" | Password, salt, folder or an advanced setting does not match. Nothing was changed. Check them against your emergency sheet. |
| "The folder … in … is empty or gone" | The vault's folder in the cloud was renamed or moved. Nothing was connected or written. Rename it back, or add the vault again from its new place. |
| Remove refuses: files still wait | Connect the vault and let the uploads finish; removing it now would lose those files. |

## Signing in to a cloud

| Problem | What to do |
|---|---|
| A cloud drive does not appear, "could not be checked in …" | The cloud cannot be reached right now (no internet?). The drive appears by itself once the cloud answers. |
| "pCloud no longer accepts the sign-in (error 2095)" | Often a passing problem at pCloud: CryptBridge asks again every minute and reconnects by itself. After a password change or "log out of all devices" it stays: click **Sign in to … again** on the Status tab, then bring the sign-in to the server with a new transfer block. |
| "… no longer accepts the sign-in: it has run out or was withdrawn" | Click **Sign in to … again** on the Status tab. Google Drive: if your Google app is still in *Testing*, Google ends every sign-in after 7 days; publish the app (step 5 of the [Google guide](google-drive-app.md)). |
| "… does not accept your own app" | The client ID or secret is wrong, or the app was deleted. Correct them on the page of the account, then sign in again. |
| Google shows "Google hasn't verified this app" | Expected with your own Google app: click **Advanced**, then **Go to … (unsafe)**. |
| "the port 53682 that receives the sign-in is in use" | Another sign-in is still waiting, perhaps in a window of rclone. Finish or close it and try again. |
| pCloud page shows "Invalid 'client_id'" during sign-in | pCloud is blocking rclone's shared access. Try again later, or use your [own pCloud app](pcloud-app.md). With your own app: check the client ID on the page of your pCloud account. |
| Browser shows "redirect_uri is not authorized" (or similar) | Add `http://localhost:53682/` to the redirect addresses of your own [pCloud](pcloud-app.md) or [Dropbox](dropbox-app.md) app. A Google desktop app needs no such address. |

## Drives and uploads

| Problem | What to do |
|---|---|
| Drive letter in use | Choose another letter on the page of the vault (while it is disconnected). |
| Quit warns about uploads | Files not yet in the vault stay on this PC and are written into it the next time you connect. Do not delete the cache folder in the meantime. |

## Vaults on this PC or a USB drive

| Problem | What to do |
|---|---|
| "The keys … do not open the folder" | Another drive got the vault's drive letter. Nothing was connected or written. |
| "Waiting for its folder" | Its drive is not plugged in (or got another letter). It connects by itself when the folder is back. If the drive now has another letter, add the vault again from there and remove the old entry. |
| "… is not the folder the drive was last connected to" | Another drive with a copy of the vault, or the vault was moved. If it is the vault you want to work with from now on, click *This is my vault*. |

## LAN access

| Problem | What to do |
|---|---|
| A device at home cannot reach the LAN access | *Settings > LAN access* shows the firewall state: allow it there if needed, and set your network to *private* in Windows if CryptBridge says it is public. Use the address shown there; the PC must be awake. See [LAN access](lan-access.md#the-firewall-check). |
| "port … is used by another program" | Choose another port (1024 to 65535) for that access and save. The two accesses need different ports. |

## Linux server

| Problem | What to do |
|---|---|
| Check codes differ between PC and server | The keys or settings differ. Bring them over again with a transfer block. |
| The video player cannot reach the server | Run `cryptbridge` on the server: it shows which service is not running. Check ports 80 and 443 at your provider. |
| A vault is missing in the video player | It is switched off or not running: run `cryptbridge` on the server. A vault that was just switched on or off can take 10 seconds to show up or disappear. |
| No certificate on the server, log says `rateLimited` | Your provider's host name shares Let's Encrypt's weekly limit with all customers. Use a DuckDNS name or your own domain and run `sudo cryptbridge setup` again (keep the streaming account). |
| "This transfer block comes from a newer CryptBridge" | Download the current `setup.sh` and run it, see [Updating the server](linux-server.md#updating-the-server). |

## Log files

- Windows: `%LOCALAPPDATA%\CryptBridge\logs\rclone.log`
- Server: `journalctl -u 'rclone-mount-*' -u rclone-webdav -u caddy`

Still stuck? [Open an issue](https://github.com/burnersen/CryptBridge/issues) and describe what you see.
Please never post your password, salt, transfer block or emergency sheet.
