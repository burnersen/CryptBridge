# Google Drive: your own Google app (required)

[← Back to the README](../README.md) · [Cloud accounts](cloud-accounts.md)

Google is retiring the shared access that all rclone users had (Google wants to charge for it during
2026). So CryptBridge signs in to Google Drive only through **an app of your own**. Setting it up takes
about 15 minutes, once, and costs nothing. One app serves all your Google accounts. The same guide is in
CryptBridge on the page for adding a Google Drive account, with Copy buttons.

1. Open the **[Google Cloud Console](https://console.cloud.google.com/projectcreate)** and sign in with
   your Google account. Create a new project, for example `CryptBridge`, and make sure it is selected at
   the top of the page.
2. Turn on the **[Google Drive API](https://console.cloud.google.com/apis/library/drive.googleapis.com)**:
   click **Enable**.
3. Open the **[Google Auth Platform](https://console.cloud.google.com/auth/overview)** and click **Get
   started**. App name `CryptBridge (personal)`, your own email address as support and contact address,
   audience **External**. Agree and click **Create**.
4. Open **[Data access](https://console.cloud.google.com/auth/scopes)**, click **Add or remove scopes**
   and add `https://www.googleapis.com/auth/drive` (tick it in the list, or paste it under *Manually add
   scopes*). Click **Update**, then **Save**.
5. Open **[Audience](https://console.cloud.google.com/auth/audience)**, add your own Google address under
   **Test users**, then click **Publish app** and confirm. If Google asks for a homepage and a privacy
   policy first, enter them under **Branding**, for example `https://github.com/burnersen/CryptBridge`.

   > ⚠️ **Important:** while the app stays in *Testing*, Google ends every sign-in after 7 days. Publish it.

6. Open **[Clients](https://console.cloud.google.com/auth/clients)**, click **Create client**, choose the
   application type **Desktop app**, name it `CryptBridge` and click **Create**.
7. Copy the **Client ID** and the **Client secret** into CryptBridge (*Add cloud account > Google Drive*)
   and click *Sign in*.
8. Google then warns that it has not verified the app. That is expected: it is your own app, which only
   you use. Click **Advanced**, then **Go to CryptBridge (personal) (unsafe)** and **Continue**.

The client ID and secret are stored encrypted with your Windows account, like your sign-ins. A
[transfer block](linux-server.md) carries them to your Linux server, so nothing has to be typed there.
