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

Needs a PostgreSQL database; the schema (including a small seeded
unlockables catalog, see [Gamification](#gamification-todo-and-account-bio)
below) is created automatically on startup.

**Locally, for testing** (Postgres already installed and running):

```bash
createdb tiru_emba_dev                      # one-time, if it doesn't exist yet
go build -o tiru-server ./cmd/tiru-server
go build .                                  # the tiru-emba client itself

./tiru-server --addr=127.0.0.1:8443 --db="postgres://localhost:5432/tiru_emba_dev?sslmode=disable"
```

No `--tls-cert`/`--tls-key` needed for this — plaintext is fine on
`localhost`, and the server just prints a warning about it. In another
terminal:

```bash
./tiru-emba --server=127.0.0.1:8443
```

That drops you on the Welcome screen against your own local server — pick
Register, go through the wizard, create an org, and you're in chat. Repeat
in a third terminal with a different handle to test two accounts talking to
each other (`--handle=@someoneelse --server=127.0.0.1:8443`), or run
`./tiru-emba --handle=@you` with no `--server` at all to test LAN-only mode
by itself.

For anything beyond your own machine, pass `--tls-cert`/`--tls-key` —
without them the server runs in plaintext and warns loudly on startup, since
passwords and session tokens would otherwise cross the network unencrypted.
See [Deploying to a VM with your own domain](#deploying-to-a-vm-with-your-own-domain)
below for getting a real certificate.

### Connecting a client

```bash
tiru-emba --handle=@alex --server=chat.example.com:8443
```

`--handle` here just seeds your LAN identity and pre-fills the relay
login/register form — it isn't a separate account flag. On launch you land
on a **Welcome** screen (Log in / Register / Forgot password):

- **Register** asks for a username, password, a *local file path* to a
  profile picture (converted to an ASCII avatar automatically — leave it
  blank to skip), and an optional security question for later recovery.
- **Log in** just needs the handle/password for an existing account.
- **Forgot password** asks for the handle, shows the security question you
  set at registration, and lets you set a new password from the answer (only
  available if you set one — skipping it at registration means no recovery).
- A previously-successful login is remembered (`~/.tiru-emba/config.json`)
  and resumed automatically on the next launch, without re-entering a
  password — but you're always asked to pick an organization on every
  launch, even then, since a resumed session doesn't imply which org should
  be active.

`--lan=false` skips LAN discovery entirely, for a relay-only client, or to
avoid a UDP port conflict when running more than one `tiru-emba` instance on
the same machine for testing.

### Organizations

There's no single global directory over the relay — you can only see or
message someone you share an organization with. The first screen after
logging in lets you create a new one or join an existing one with an invite
code; from inside chat, the same is available via commands:

```
/org create Acme        # you're now Acme's admin
/org invite <id>        # generates a redeemable invite code, valid 7 days
/org join <code>        # (a teammate, on their own client) redeems it
/org list               # organizations you belong to
```

Once you share an org, that teammate appears in the sidebar's **Org**
section, and `@handle` messages or a plain broadcast reach them the same way
LAN peers already do.

### Gamification, todo, and account bio

- **Points**: sending a relay message or completing a shared todo earns a
  few points, tracked server-side per account.
- **`/account bio`**: your handle, ASCII avatar, points, and orgs. In
  relay mode this opens a full screen that also lets you browse and redeem
  a small catalog of unlockable ASCII avatars/borders with those points, and
  equip whichever one you've unlocked. In LAN-only mode (no `--server`) it
  just prints a smaller local-only summary inline, since points/orgs/the
  shop don't exist without a server.
- **`/todo`**: opens a full-screen task list — a personal section (stored
  locally, works with or without a server) and, once you're in an org, a
  shared section visible/editable by everyone in it. `/todo add <text>` is
  a quick shortcut that adds a personal item without opening the screen.

### Known limitations

- `/play tictactoe`/`/play ludo`'s networked modes and file transfer are
  LAN-only for now — none of them work over the relay yet.
- One active connection per account at a time; logging in again elsewhere is
  rejected rather than taking over the existing session.

## Deploying to a VM with your own domain

This walks through putting `tiru-server` on a real machine with a real
domain and a real TLS certificate, rather than running it on `localhost` for
local testing.

### 1. Get a VM and point DNS at it

Any provider works (DigitalOcean, Hetzner, a free-tier AWS/GCP instance,
etc.) — you just need a public IPv4 address and SSH access. Ubuntu/Debian
is assumed below. Once you have the IP, create an **A record** for the
subdomain you want (e.g. `chat.yourdomain.com` → `203.0.113.10`) with
whoever hosts your domain's DNS, and wait for it to propagate (`dig
chat.yourdomain.com` should return that IP).

### 2. Install Postgres and Go, build the server

On the VM:

```bash
sudo apt update && sudo apt install -y postgresql golang-go git
sudo -u postgres createuser tiru_emba
sudo -u postgres createdb tiru_emba -O tiru_emba
sudo -u postgres psql -c "ALTER USER tiru_emba WITH PASSWORD 'pick-a-real-password';"

git clone https://github.com/NotTesfamichael/tiru-emba /opt/tiru-emba
cd /opt/tiru-emba && go build -o /usr/local/bin/tiru-server ./cmd/tiru-server
```

(A managed Postgres instance from your cloud provider works too — just
point `--db` at it instead of installing Postgres locally.)

### 3. Get a real TLS certificate (Let's Encrypt)

`tiru-server` reads a plain cert/key file pair — no built-in ACME client —
so [certbot](https://certbot.eff.org)'s standalone mode is the simplest way
to get one, since `tiru-server` itself doesn't need port 80:

```bash
sudo apt install -y certbot
sudo certbot certonly --standalone -d chat.yourdomain.com
# certs land in /etc/letsencrypt/live/chat.yourdomain.com/{fullchain,privkey}.pem
```

Those files are root-owned and only readable by root, so either run
`tiru-server` as root (simplest, fine for a small self-hosted server), or add
a certbot [deploy
hook](https://eff-certbot.readthedocs.io/en/stable/using.html#renewal) that
copies the renewed cert somewhere your service user can read and restarts
the service. Certbot auto-renews via a systemd timer it installs itself
(`systemctl list-timers | grep certbot`) — no separate cron job needed.

### 4. Run it as a systemd service

`/etc/systemd/system/tiru-server.service`:

```ini
[Unit]
Description=tiru-emba relay server
After=network.target postgresql.service

[Service]
ExecStart=/usr/local/bin/tiru-server \
  --addr=:8443 \
  --db=postgres://tiru_emba:pick-a-real-password@localhost:5432/tiru_emba?sslmode=disable \
  --tls-cert=/etc/letsencrypt/live/chat.yourdomain.com/fullchain.pem \
  --tls-key=/etc/letsencrypt/live/chat.yourdomain.com/privkey.pem
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now tiru-server
sudo journalctl -u tiru-server -f   # watch it start, confirm "listening on"
```

### 5. Open the firewall

```bash
sudo ufw allow 8443/tcp   # or your cloud provider's security-group/firewall UI
```

(Port 80 only needs to be open transiently for certbot's standalone
challenge — it doesn't need to stay open for `tiru-server` itself, which
only uses `--addr`'s port.)

### 6. Connect from a client, anywhere

```bash
tiru-emba --handle=@alex --server=chat.yourdomain.com:8443
```

No VPN, no shared Wi-Fi needed — this works from any network. Everything
under [Connecting a client](#connecting-a-client) above (the Welcome
screen, accounts, orgs) applies the same way against a real deployed server
as it does against `localhost`.

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
