# LAN access: your vaults on every device at home

[← Back to the README](../README.md)

**Watch your films on the TV at home.** CryptBridge can share the connected drives with the other
devices in your home network (LAN): a TV or Apple TV with a video player such as Infuse or Kodi, a phone,
a tablet, a Mac. Every connected drive appears there as a folder, protected with a user name and
password. The devices connect over **WebDAV**, a common standard for sharing files over a network that
most video players and file apps understand.

> [!WARNING]
> Inside your home network the files travel **unlocked**, protected by the password only. Do not turn the
> LAN access on in networks you do not trust, such as a hotel or café, and never open it to the internet.

## Two accesses

There are **two accesses**, each with its own user name and password, turned on separately at
*Settings > LAN access*:

| Access | Suggested port and user | What the devices may do | For |
|---|---|---|---|
| **Streaming access** | `8080`, `stream` | open, play and copy files, but never change or delete anything | TVs and video players |
| **Full access** | `8081`, `full` | also upload, rename and delete | file apps on devices you trust |

The **port** is like a door number on your PC; change it only if another program already uses it. The
address then looks like `http://192.168.1.20:8080/`, and the Settings page shows the right one for your
PC. CryptBridge suggests passwords that are easy to type with a TV remote, and refuses the same password
for both accesses: the streaming password is typed on TVs, where others may see it, and must never open
the full access.

## Using it

- **On your devices:** in a video player (Infuse, Kodi), add a WebDAV share with the address, port, user
  name and password of the **streaming access** (protocol `http`, path `/`). A file app on a phone or
  tablet gets the **full access** the same way. On a Mac: *Finder > Go > Connect to Server*.
- **It follows the drives.** A drive that connects appears in the network; a drive leaves the network
  before it disconnects. Locking CryptBridge ends the LAN access at once. The PC has to be switched on
  and awake.
- **Only your home network.** When you turn it on, Windows asks once for permission (the usual
  administrator prompt) to let the devices of your home network in. This works only if Windows counts
  your home network as **private**. If it does not, CryptBridge tells you; change it in *Windows Settings
  > Network & internet > your connection > Network profile type*.
- **Other Windows PCs** do not accept this kind of password-protected address without changing their
  registry. Install CryptBridge there instead.

## The firewall check

The LAN access page checks the Windows firewall for you and says plainly what it finds:

- whether **CryptBridge's own rule** is there and fits the ports that are on (*Allow in the firewall*
  adds or updates it);
- whether Windows counts your network as **public**, where the rule does not apply;
- whether **other rules block** rclone, which win over any rule that allows;
- whether **older rules let rclone in everywhere**, beyond your home network. Windows creates such rules
  when rclone is allowed once. CryptBridge offers to remove them (Windows asks for permission).

If rclone is updated through winget, it moves to a new folder; the page then offers *Allow in the
firewall* once more.

<details>
<summary><b>Technical details: firewall rule and changes from the network</b></summary>

- CryptBridge asks Windows for one firewall rule: inbound connections to the ports of the accesses that
  are on, for rclone only, from devices of the same network, and only on networks Windows counts as
  private. Turning both accesses off removes the rule again (Windows asks once more); turning the second
  one on or changing a port updates it.
- The connection inside the home network is plain HTTP, without TLS.
- Changes from the network (full access only) go into the drive's upload queue, like files copied in
  Explorer.
- The LAN passwords are stored like the vault keys: encrypted with your Windows account, and sealed with
  the app password when the app lock is on.

</details>

## Away from home: VPN into your home network

Never open the LAN access to the internet on your router. To use it on the road, connect your phone,
tablet or laptop to your home network through a **VPN** (a secure tunnel into your home network). The
device then reaches the LAN access as if it were at home, the VPN encrypts the whole way, and your own PC
at home does the job of a rented server. How smoothly films play then depends on the upload speed of
your internet connection at home.

The VPN has to give your devices an address of your home network:

- **The VPN of your router** (WireGuard), for example of a FRITZ!Box: works, nothing to change in
  CryptBridge.
- **Tailscale with a subnet router** in your home network (a router, NAS or Raspberry Pi that makes
  your home network reachable): works as well.
- **Tailscale running only on this PC** is *not* supported yet: its devices get addresses of their own
  (100.x.x.x), which CryptBridge's firewall rule does not let in.

*Not tested yet.* Reports are welcome.

Want to stream without your PC at home running? See the [Linux server](linux-server.md).
