# Cloud accounts

[← Back to the README](../README.md)

Add your clouds at *Vaults tab > Add cloud account*. Each account gets its own page there, with the
sign-in, the free space, a name you can change, and *Remove from CryptBridge* (possible once no vault
lies in it; your files in the cloud are never touched). You can add several accounts of the same
service, for example a private and a work Google Drive.

| Service | Sign-in | Own app |
|---|---|---|
| **[pCloud](https://www.pcloud.com)** | in the browser; CryptBridge finds out by itself whether your account is in the EU or the US | optional, recommended: [own pCloud app](pcloud-app.md) |
| **[Google Drive](https://drive.google.com)** | in the browser | **required**: [your own Google app](google-drive-app.md) |
| **[OneDrive](https://onedrive.live.com)** | in the browser with your Microsoft account | not possible with a personal Microsoft account, so rclone's access is used |
| **[Dropbox](https://www.dropbox.com)** | in the browser | optional: [own Dropbox app](dropbox-app.md) |

## Signing in

Signing in works the same everywhere: CryptBridge opens the sign-in page of the service in your
browser, you sign in there and allow the access, and CryptBridge continues by itself. Your password for
the cloud is typed only on the page of the service; CryptBridge never sees it. If the service has a
question afterwards (OneDrive, for example, if your account has more than one drive), CryptBridge shows it
in a small window. If you close the browser instead, click *Cancel* in CryptBridge.

**Sign-ins stay fresh.** Some clouds (Microsoft above all) hand out a new sign-in every time the old one
is renewed. CryptBridge keeps the new one, so you do not have to sign in again every few months. If a
cloud refuses the sign-in, the Status tab says so and offers **Sign in to … again**.

## Good to know

- **Tested (October 2026)** with pCloud, Google Drive, OneDrive and Dropbox at the same time, including
  films streamed from all of them.
- **OneDrive:** the *Personal Vault* of OneDrive and files *shared with you* cannot be used for a vault.
  Choose a normal folder.
- **Dropbox:** CryptBridge uploads files to Dropbox one by one. Dropbox may slow this down a little when
  you copy very many small files at once; rclone then waits and carries on by itself.
- **OneDrive and Dropbox** do not tell upper from lower case in file names, so vaults with *base64* file
  names are refused there.
- **Linux server:** the [server setup](linux-server.md) works with vaults in all four clouds (since
  CryptBridge 0.8).

## Updating from CryptBridge 0.6 or older

Your pCloud account and your vaults are taken over by themselves when CryptBridge 0.7 or newer starts
(with the app lock: when you unlock it). The old settings stay next to the new ones as
`settings.dat.v2-backup`. Older versions of CryptBridge cannot read the new settings.

## Your own apps

| Service | Guide | Why |
|---|---|---|
| Google Drive | [Your own Google app](google-drive-app.md) | **required**: Google is retiring the shared rclone access |
| pCloud | [Own pCloud app](pcloud-app.md) | recommended: pCloud has blocked the shared rclone access before |
| Dropbox | [Own Dropbox app](dropbox-app.md) | optional: independent of the limits of the shared access |
