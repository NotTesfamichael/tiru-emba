# tiru-emba

Zero-configuration, LAN-only terminal chat. Run it on any machine on the same
Wi-Fi network — teammates are discovered automatically via UDP multicast, no
server, no config file. An optional [relay
server](#cross-network-chat-relay-server) adds cross-network chat for
teammates who aren't on that LAN, without changing anything about how LAN
mode itself works.

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

Teammates on the same Wi-Fi network show up automatically in the sidebar,
each assigned a stable color derived from their handle (same person, same
color, for everyone, every session). Send a direct message with `@handle your
message` — it's delivered straight to that peer over TCP (`--port`, default
`7777`, is what your own client listens on for incoming messages).

- **Multiple recipients**: list several handles before the message body —
  `@kal @sam are we still on for 3pm` sends that text to both.
- **Autocomplete**: start typing `@` and matching online handles appear above
  the input; `Tab` accepts the highlighted one, `↑`/`↓` cycle through matches.
- **Notifications**: an incoming message triggers a desktop notification with
  sound. Best-effort — a machine with no notification daemon (e.g. a headless
  SSH session) just skips it silently rather than failing.
- **Filter by conversation**: `/filter @kal` hides everything except your
  conversation with `@kal` (which also means "only that color"); `/clear`
  shows everything again.
- **Persistent history**: every direct message (sent and received) is saved
  to `~/.tiru-emba/history/<handle>.jsonl` and reloaded on the next launch —
  losing Wi-Fi or closing the app doesn't lose your conversation. Join/leave
  notices and errors are session-only and aren't saved.

### File transfer

Drag a file onto the terminal window (most terminal emulators insert the
absolute path as text) after typing the recipient, or just type the path
yourself — `@kal /Users/you/Downloads/photo.png` — and press Enter. This
sends an *offer*, not the file itself:

1. `@kal` sees `<you> wants to send photo.png (2.3 MB) — accept? [y]es [n]o`
   and must explicitly respond — nothing transfers without that.
2. Only on accept does the file actually stream over TCP.
3. Accepted files are saved into `~/Downloads`, with `(1)`, `(2)`, ...
   appended if a same-named file is already there.

Safety limits: transfers are capped at 200MB, and an incoming filename is
always reduced to just its base name before being saved — a peer can't make
your client write outside `~/Downloads` no matter what path it claims.

### Games

`/play tictactoe @kal` challenges an online peer. Like a file offer, nothing
starts until they respond to `<you> challenges you to tictactoe — accept?
[y]es [n]o`. On accept, both clients switch to a full-screen Tic-Tac-Toe
board — arrows or WASD to move the cursor, Enter to place your mark, Esc to
resign. The connection used for the invite stays open for the whole game (it
isn't a fire-and-forget message, unlike chat), so moves land on the
opponent's board immediately. When the game ends (win, draw, or a
resignation), both sides drop back into chat with a note about how it went.

More games are meant to build on this same foundation later, hence the
explicit `tictactoe` argument in the command rather than a bare `/play @kal`.
There's also a local Ludo mode (`/play ludo`, 2-4 players against simple AI
opponents in the same terminal) and a networked one (`/play ludo @handle
[@handle ...]`, up to 4 real players over LAN) — both LAN-only for now, see
[Known limitations](#known-limitations) below.

## Cross-network chat (relay server)

LAN discovery only works when everyone's on the same Wi-Fi network. For
teammates elsewhere, run a relay server somewhere reachable by everyone and
point clients at it with `--server` — this is additive: a connected client
sees LAN peers and relay teammates in the sidebar at the same time, in
separate **LAN** and **Org** sections.

### Running the server

Needs a PostgreSQL database; the schema is created automatically on startup:

```bash
go build -o tiru-server ./cmd/tiru-server
./tiru-server --addr=:8443 --db="postgres://user:pass@host:5432/dbname"
```

Pass `--tls-cert`/`--tls-key` for anything beyond local testing — without
them the server runs in plaintext and warns loudly on startup, since
passwords and session tokens would otherwise cross the network unencrypted.

### Connecting a client

```bash
tiru-emba --handle=@alex --server=chat.example.com:8443 --server-register
```

`--server-register` creates a new account the first run; drop it on later
runs to log into the existing one instead. Either way you're prompted for
the account's password (masked, never echoed — separate from anything typed
on the actual server machine). `--lan=false` skips LAN discovery entirely,
for a relay-only client, or to avoid a UDP port conflict when running more
than one `tiru-emba` instance on the same machine for testing.

### Organizations

There's no single global directory over the relay — you can only see or
message someone you share an organization with. Whoever creates one becomes
its admin:

```
/org create Acme        # you're now Acme's admin
/org invite <id>        # generates a redeemable invite code, valid 7 days
/org join <code>        # (a teammate, on their own client) redeems it
/org list               # organizations you belong to
```

Once you share an org, that teammate appears in the sidebar's **Org**
section, and `@handle` messages or a plain broadcast reach them the same way
LAN peers already do.

### Known limitations

- `/play tictactoe`/`/play ludo`'s networked modes and file transfer are
  LAN-only for now — none of them work over the relay yet.
- One active connection per account at a time; logging in again elsewhere is
  rejected rather than taking over the existing session.

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
