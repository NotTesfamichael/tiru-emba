# tiru-emba

Zero-configuration, LAN-only terminal chat. Run it on any machine on the same
Wi-Fi network — teammates are discovered automatically via UDP multicast, no
server, no config file.

## Installation

### go install

Works today, no release needed:

```bash
go install github.com/NotTesfamichael/tiru-emba@latest
```

### Homebrew (macOS / Linux)

```bash
brew install --cask NotTesfamichael/tiru-emba/tiru-emba
```

### Debian / Ubuntu (.deb)

Each [release](https://github.com/NotTesfamichael/tiru-emba/releases) attaches
`.deb` packages for amd64/arm64:

```bash
curl -LO https://github.com/NotTesfamichael/tiru-emba/releases/latest/download/tiru-emba_linux_amd64.deb
sudo dpkg -i tiru-emba_linux_amd64.deb
```

`.rpm` (Fedora/RHEL) and `.apk` (Alpine) packages are published the same way.

> A real `sudo apt install tiru-emba` needs these `.deb`s hosted in an actual
> apt repository (a PPA, or a self-hosted repo via `aptly`/GitHub Pages) —
> see [Publishing](#publishing--one-time-setup) below.

### Snap

```bash
sudo snap install tiru-emba
```

### From source

```bash
git clone https://github.com/NotTesfamichael/tiru-emba
cd tiru-emba
go build .
```

## Usage

```bash
tiru-emba --handle=@alex
```

Teammates on the same Wi-Fi network show up automatically in the sidebar.
Send a direct message with `@handle your message` — it's delivered straight
to that peer over TCP (`--port`, default `7777`, is what your own client
listens on for incoming messages).

## Troubleshooting: "Online (0)", teammates not showing up

Discovery uses UDP multicast, with UDP broadcast as a fallback (some
consumer/mesh Wi-Fi routers relay one more reliably than the other between
wireless clients). If you still see nobody:

- **macOS Application Firewall**: it can silently drop incoming discovery
  traffic to `tiru-emba` without ever showing the "allow incoming
  connections?" prompt, especially since every release is a freshly-built,
  differently ad-hoc-signed binary the firewall doesn't recognize yet. Check
  System Settings → Network → Firewall, and either turn it off to confirm
  that's the cause, or explicitly allow `tiru-emba`.
- **macOS Local Network permission**: System Settings → Privacy & Security →
  Local Network — this is granted per *terminal app* (Terminal, iTerm2,
  Ghostty, ...), not per CLI tool, so check whichever terminal you're running
  `tiru-emba` from.
- **Client/AP isolation**: some routers (especially mesh systems and guest
  networks) block device-to-device traffic entirely, multicast/broadcast
  included. If teammates can't even `ping` each other's IP, this is a router
  setting, not something the app can work around.

## Publishing / one-time setup

Releases are built by [GoReleaser](https://goreleaser.com) via
[`.github/workflows/release.yml`](.github/workflows/release.yml) whenever a
`v*` tag is pushed:

```bash
git tag v0.1.0
git push origin v0.1.0
```

That alone gets you: cross-compiled binaries, checksums, and `.deb`/`.rpm`/`.apk`
attached to the GitHub Release — no extra setup. Two channels need a one-time
manual step first, because they require credentials I can't create on your
behalf:

**Homebrew tap**
1. Create an empty repo named `homebrew-tiru-emba` under this account.
2. Create a GitHub [PAT](https://github.com/settings/tokens) with `repo` scope
   on that tap repo, and add it as the `HOMEBREW_TAP_GITHUB_TOKEN` secret in
   this repo's Settings → Secrets → Actions.

**Snap Store**
1. `snapcraft login`, then `snapcraft register tiru-emba` (one-time name
   claim).
2. `snapcraft export-login snapcraft.creds` and paste the file contents into
   this repo's `SNAPCRAFT_STORE_CREDENTIALS` secret.

Until those secrets exist, the workflow automatically skips just those two
steps (see the `Compute --skip flags` step) rather than failing the release.

**apt/yum repo hosting** — not automated here on purpose, since it involves
generating and safely custodying a GPG signing key. The pragmatic options,
roughly in order of effort:
- [packagecloud.io](https://packagecloud.io) (free for open source) — push the
  `.deb`/`.rpm` GoReleaser already builds; it handles signing and repo hosting.
- A Launchpad PPA (Ubuntu-only, requires a source-package build via `dput`).
- Self-hosted via `aptly`/`reprepro` publishing to GitHub Pages, with a GPG key
  you generate and store as a repo secret.
