# Own pCloud app (recommended)

[← Back to the README](../README.md) · [Cloud accounts](cloud-accounts.md)

CryptBridge talks to pCloud through rclone. Out of the box it uses **rclone's shared pCloud access**,
which all rclone users share. pCloud has blocked it for hours before: on 5 October 2026, for about
three hours, the browser showed *Invalid 'client_id'* and existing sign-ins failed with *error 2095*.

With an **app of your own** (free, at pCloud), CryptBridge no longer depends on that shared access.
**Your vaults do not change:** same keys, same files, same check codes. You only sign in once more.

> **Tested (October 2026)** with an approved pCloud app: the sign-in works, and the pCloud drives connect
> through it. The own app is optional: CryptBridge also works right away with rclone's shared access.

> [!TIP]
> Request the app **now, while everything works**: pCloud has to approve it, which took about a day for
> me.

1. Open **[My Apps on pCloud](https://docs.pcloud.com/my_apps/)**, log in with your pCloud account and
   request a new application.
2. Fill in the form like this and click **Create request**:

   | Field | Enter |
   |---|---|
   | App name | `CryptBridge (personal)` |
   | Application Type | **Personal use** |
   | Folder access | **All folders**, because your vaults are normal folders in your pCloud (*Private* would most likely limit the app to a folder of its own) |
   | Write access | **Yes**, needed to upload files |
   | Website | `https://github.com/burnersen/CryptBridge` |
   | Expected number of users | `1` |
   | Reason | the text below (also in CryptBridge on the page of your pCloud account, with a Copy button) |

   ```text
   Personal use only. I use the open-source tool CryptBridge (https://github.com/burnersen/CryptBridge), which is based on rclone, to access my own pCloud account: it shows an encrypted folder of my pCloud as a drive on my own computers. Only my own pCloud account will use this app. It needs access to all folders because my existing encrypted folders are not inside an app folder, and write access to upload my files.
   ```

3. Once pCloud has approved the app, open it on My Apps, switch to **Settings** and enter
   `http://localhost:53682/` under **Redirect URIs**, exactly like this. Without it, the sign-in ends with
   *redirect_uri is not authorized*. Set *Allow implicit grant* to **Disallow** (CryptBridge does not need
   it). Publisher, description and icon can stay empty. Click **Save changes**.
4. In CryptBridge, on the page of your pCloud account (*Vaults tab*, under *Cloud accounts*), section
   *Own pCloud app*: paste the **Client ID** and the **Client secret**, click **Save app** and confirm the
   new sign-in. Connected pCloud drives are disconnected for the sign-in and connected again afterwards.
   The page then says *… through your own app*.
5. **Linux server** (not urgent: its sign-in keeps working until pCloud blocks rclone's shared access
   again): create a new transfer block on the page of the vault, run `sudo cryptbridge vault add` on the
   server and paste the block. The vault on the server takes the new sign-in. (Server still on
   CryptBridge 0.8 or older: run the current `setup.sh` once first, see
   [Linux server](linux-server.md#setup).)

Client ID and secret are stored encrypted with your Windows account like the rest of the settings. They
are used only for signing in and are not part of the transfer block: the server needs only the sign-in.
