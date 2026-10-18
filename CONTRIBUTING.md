# Contributing to CryptBridge

Thank you for wanting to help! CryptBridge is a small spare-time project, and every bug report, test
and pull request is welcome.

## Ways to help

- **Test and report bugs.** Open an [issue](https://github.com/burnersen/CryptBridge/issues) with your
  Windows version, the cloud, what you did and what happened. Attach the relevant part of
  `%LOCALAPPDATA%\CryptBridge\logs\rclone.log`, with passwords, tokens and file names removed.
- **Add a cloud.** CryptBridge supports pCloud, Google Drive, OneDrive, Dropbox and MEGA so far. rclone
  knows many more, and support for them is the most wanted help. Use the issue template
  *Support for a new cloud* to say which one you would like to add or see.
- **Improve the guides.** The docs in [`docs/`](docs) are written for people who are not technical.
  Clearer words and fixed steps help a lot.

For larger changes, please open an issue first, so we can agree on the way before you spend time on it.

## Building and testing

You need Go (the version set in [`windows/go.mod`](windows/go.mod)) and PowerShell 7.

```powershell
pwsh -File windows\build.ps1        # builds the Windows program
cd windows; go test ./...           # tests of the Windows program
```

```bash
bash linux/tests/functions_test.sh  # tests of the Linux setup
```

The window is plain HTML, CSS and JavaScript in [`windows/frontend/dist`](windows/frontend/dist), with
no build step. The version lives in one place, the file [`VERSION`](VERSION); the build script writes
it everywhere else. More background: [Technical details](docs/technical.md).

## Adding a cloud

The supported clouds are a table in
[`windows/internal/cloud/service.go`](windows/internal/cloud/service.go). A new cloud starts with one
entry there: rclone's backend name, how the user signs in, whether an own app (client ID) is optional,
required or not offered, and the answers to rclone's follow-up questions for that backend. The commit
*Add MEGA as the fifth cloud* shows everything a new cloud touched, from sign-in to the Linux setup
and the guides.

Before a cloud is added, please test with a real account:

- signing in, adding a new vault and opening an existing one
- copying large files in and out, and streaming a film from the drive
- what happens when the sign-in expires or the connection drops

## Pull requests

- Keep each pull request to one change, and run the tests above before you send it.
- Write code that reads like the code around it, and add tests for new behaviour.
- New source files start with the same three-line header as the others
  (`SPDX-License-Identifier: GPL-3.0-only`).
- Guides are written in plain, simple English for people who are not technical.

## License

CryptBridge is under the [GNU General Public License, version 3 only](LICENSE). By sending a pull
request, you agree that your contribution is published under the same license.
