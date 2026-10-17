# Own Dropbox app (optional)

[← Back to the README](../README.md) · [Cloud accounts](cloud-accounts.md)

Without an app of your own, CryptBridge uses rclone's shared access to Dropbox, which works. Your own app
keeps you independent of the limits that access shares with every rclone user. It takes about five
minutes and needs no approval:

1. Open the **[Dropbox App Console](https://www.dropbox.com/developers/apps)**, sign in and click
   **Create app**. Choose **Scoped access** and **Full Dropbox**, and give it a name of your own, for
   example "CryptBridge" followed by your name (Dropbox wants names that nobody else uses).
2. On the **Permissions** tab, tick `account_info.read`, `files.metadata.write`, `files.content.write`,
   `files.content.read` and `sharing.write`, then click **Submit**.
3. On the **Settings** tab, add `http://localhost:53682/` under **OAuth 2 Redirect URIs** and click
   **Add**.
4. In CryptBridge, enter the **App key** as *Client ID* and the **App secret** as *Client secret*: when
   you add the account, or later on the page of your Dropbox account (then sign in again).

The app key and secret are stored encrypted with your Windows account, like your sign-ins. A
[transfer block](linux-server.md) carries them to your Linux server, so nothing has to be typed there.
