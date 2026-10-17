# Security

[← Back to the README](../README.md)

How CryptBridge protects your files, what it cannot do for you, and what only you can do.

## What only you can do

> [!IMPORTANT]
> 1. **Keep the password and the salt safe.** Print each vault's **emergency sheet** and keep it like a
>    house key. Without the password and the salt nobody can open the vault: not your cloud provider, not
>    CryptBridge, not me. There is no "forgot password" for a vault.
> 2. **Turn on the app lock** (*Settings > App lock*). Without it, anyone who uses your Windows account,
>    or sits at your PC while it is unlocked, can open your drives and print your emergency sheets.
> 3. **Turn on BitLocker** for the disk with the cache folder. The cache holds readable copies of your
>    files, Windows remembers recently opened file names and preview pictures, and deleted files can be
>    recovered from an unencrypted disk. Lock Windows when you leave the PC.
> 4. **Use the LAN access only in your home network.** There the files travel unlocked, protected by a
>    password only. Never open it to the internet; use a VPN into your home network instead.

## What makes it safe

- **Your files are locked on your PC.** The cloud provider, and anyone who might ever get into your
  account, only sees scrambled names and contents. A vault is an [rclone crypt](https://rclone.org/crypt/)
  folder, locked with a **password** and a **salt** (a second password) of your choice.
- **Wrong keys are caught before they can do harm.** rclone never tells you "wrong password". With
  wrong keys (password and salt), a vault simply looks empty, and everything you copy into it would be
  locked with the wrong keys. Your vault would then be split into two parts that no single password
  opens. That is why CryptBridge tests the keys when you add a vault and every time before its drive
  appears, and refuses keys that do not fit. A small [check file](#the-file-cryptbridge-vault) in every
  vault helps with this.
- **No vault is overwritten by mistake.** A new vault is only created in an empty folder, never inside
  another vault, and two vaults never share a folder. A vault on a USB drive connects only to its very
  own folder, not to a copy on another stick that got the same drive letter, and its drive closes at
  once when the folder disappears.
- **No typing errors between PC and server.** The Windows program packs sign-in and keys into an
  encrypted *transfer block*, a piece of text you paste into the server setup together with a one-time
  code. The same **check code** on both sides proves that the keys match.
- **Your keys stay yours.** On Windows, CryptBridge's settings are encrypted with your Windows account
  (DPAPI), and rclone keeps its configuration in memory only. With the optional
  [app lock](windows-app.md#app-lock-recommended), they are also sealed with a password of your own
  (Argon2id and AES-256). On the server, only your user can read the keys.
- **Locked means nothing readable is left behind.** With the app lock on, locking CryptBridge closes all
  drives, ends the LAN access and deletes the cache with its readable copies.
- **Uploads are never cut off silently.** CryptBridge shows running uploads and warns you before you
  disconnect or quit while they run.
- **The drive survives problems.** rclone runs as a program of its own: if the window crashes, the drive
  stays; if rclone stops, CryptBridge brings the drive back. If a cloud refuses the sign-in, CryptBridge
  says so plainly, asks again every minute and reconnects by itself once the cloud accepts it again.
  Without internet, a cloud drive does not appear at first, because its keys cannot be tested yet. It
  appears by itself as soon as the cloud can be reached again.
- **Sign-ins stay fresh.** Some clouds (Microsoft above all) hand out a new sign-in every time the old
  one is renewed. CryptBridge keeps the new one, so you do not have to sign in again every few months.
- **No lock-in.** The emergency sheet lists the plain rclone commands that open the vault without
  CryptBridge. The source code is open, and rclone is a free tool of its own.
- **The streaming server cannot delete anything.** Its access is read-only, and repeated wrong passwords
  are blocked by fail2ban. See [Linux server](linux-server.md#what-the-setup-does).

## The file `.cryptbridge-vault`

Every vault holds a small file named `.cryptbridge-vault`. It is not clutter. CryptBridge uses it to
make sure that the keys you entered really belong to this vault: only with the right keys can it find
and read this file. The Windows program looks for it when you add the vault and every time before its
drive appears, the Linux setup when it sets up the server. The file contains just two lines of text
that say what it is for.

You do not see it on the drives, in the LAN access or in the server's vault folders, but it stays in the
vault. You may still meet it where the vault is opened another way, for example with plain rclone.

**Please keep it.** Deleting it does not harm your files. The key test still works without it, using
some of your other files, but it is less certain until the file is back. The Windows program puts it
back by itself the next time the drive connects.

## What the app lock cannot do

Windows itself remembers recently opened file names and preview pictures, and deleted files can be
recovered from an unencrypted disk. Turn on **BitLocker** for the disk with the cache folder, and lock
Windows when you leave the PC. Everything about the app lock: [Windows program](windows-app.md#app-lock-recommended).

## No warranty

CryptBridge comes with **no warranty** (see the [license](../LICENSE)). Keep your emergency sheet:
without the password and the salt, nobody can open the vault.
