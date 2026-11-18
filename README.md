# Agent Companion

Run and follow coding agents on your own computers from an Android phone. It supports
**[Claude Code](https://claude.com/claude-code)** and **agy** (Google's Antigravity CLI). You can chat with
them, approve tool calls, answer their questions, review plans and diffs, and use real terminals, on any
number of computers at once. Every connection is end-to-end encrypted.

**[Download the Android app](https://github.com/Sidd065/agent-companion/releases/latest/download/agent-companion.apk)**
· [Releases](https://github.com/Sidd065/agent-companion/releases) · [Install](#install)

```
 Android app ──── wss:// (Cloudflare quick tunnel) ────▶ cloudflared ─▶ agentc ──┬─ claude -p     (one per Claude session, stream-json)
  (Kotlin/Compose)   end-to-end encrypted (Noise IK)                   (Go)     ├─ agy acp       (one per agy session)
        ▲                                                                       ├─ agy language server (tasks, artifacts…)
        │                                                                       ├─ ~/.claude      (history, checkpoints, subagents)
        └────────────── Firebase Cloud Messaging (notifications) ◀──────────────┴─ tmux          (terminals: claude, agy, shells)
```

Each computer runs a small daemon, `agentc`, which drives the agents and reaches the phone through a
Cloudflare quick tunnel. You don't need port forwarding, a VPN or an account. The app shows the sessions
and terminals of all your computers together.

## Features

**Both agents**

- **Chat sessions** in any folder on any computer. Send text, photos and files; stop a turn; queue
  follow-ups or send them while the agent works. Switch model, effort and mode, and run slash commands.
- **Live transcript**: streaming answers and thinking, tool calls with diffs, plans, to-dos and usage.
  Long-press a message to copy it, select part of it, fork or rewind from it.
- **Approvals**: allow or deny tool calls (optionally with a comment), answer questions and approve plans,
  including straight from notifications.
- **Ongoing work**: background shells with live output, subagents with their transcripts, and Stop
  buttons for both. When the agent picks work back up by itself (a background task reported back), the
  session shows as working and you're notified when it's done.
- **Side questions** (`/btw`): ask about the conversation without adding to it, even while the agent works.
  Answers open full screen, with copyable code blocks and readable tables. You can select any part,
  share it, quote it into your next message or ask a follow-up. Answers wait for you if you leave.
- **Artifacts and plans**, rendered, with comments on any paragraph, step or code block. You can also
  comment on single lines in the diff viewer.
- **History**: every conversation on each computer, with fork, rewind (previewing what it undoes), rename,
  pin, archive, delete, export and per-file diffs.
- **Files**: browse, edit, upload, download, create, rename, move and delete files on any computer.
- **Terminals**: a full terminal emulator for shells and the agents' own TUIs, with "needs you" status.
  Terminals keep running when you leave, and the laptop can attach to the same session (`claudr`, `agyr`).
- **Launch flags** per agent (skip permissions, model, effort, permission mode, extra folders, …), saved on
  the phone and reused for new sessions and terminals.
- **Commands**: the agent's own slash commands, plus `/status`, `/tasks`, `/btw`, `/model`, `/effort`,
  `/mode`, `/rewind`, `/fork`, `/diff`, `/export` and more; for Claude Code also `/permissions` and
  `/memory`.

**Claude Code**

- **Permissions the way Claude asks**: its own options, including *always allow* rules such as
  `Bash(npm test:*)`, and the reason it gives. A comment on a denial reaches Claude as your instruction.
- **Questions** on a card with options, multi-select and *Other*. **Plans** on a card: approve with
  auto-accepted or manual edits, send them back with feedback, or comment on the plan file.
- **Steering**: messages sent while Claude works arrive mid-turn, as in the terminal.
- **Modes and effort**: ask, accept edits, plan, auto, don't ask and bypass, and effort from low to max.
  Every model Claude Code lists is available, and the app shows cost per turn, a context meter and the full
  `/context` breakdown.
- **Rewind** restores files from Claude's checkpoints as well as the conversation; **fork** branches at any
  message. Turns Claude starts on its own, when background agents report back, appear as they happen.
- **Your terminal sessions too**: conversations started with `claude` on the laptop are in the history.
  While one is open in a terminal, the phone follows it live, read-only (fork it to continue). Sessions
  started from the phone show up in `claude --resume`.
- **MCP servers** (status, enable/disable, reconnect) and, on the Host screen, the Claude Code version,
  plan usage limits (5-hour and weekly), settings.json and `claude update`.

## Install

### 1. On each computer

You need Linux with systemd, [`cloudflared`](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/),
`tmux`, and at least one agent: [Claude Code](https://claude.com/claude-code) (signed in) and/or `agy`.

```sh
arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
curl -fLO "https://github.com/Sidd065/agent-companion/releases/latest/download/agentc-linux-$arch"
curl -fsSL https://github.com/Sidd065/agent-companion/releases/latest/download/SHA256SUMS | sha256sum -c --ignore-missing
chmod +x "agentc-linux-$arch" && "./agentc-linux-$arch" install

agentc status                    # shows the tunnel URL once cloudflared is up
loginctl enable-linger "$USER"   # optional: keep agentc running while you're logged out
```

`install` copies the binary to `~/.local/bin/agentc`, adds the `claudr` and `agyr` helpers there, and starts
the `agentc` systemd user service. It needs `~/.local/bin` on your `PATH`.

Direct downloads: [agentc-linux-amd64](https://github.com/Sidd065/agent-companion/releases/latest/download/agentc-linux-amd64)
· [agentc-linux-arm64](https://github.com/Sidd065/agent-companion/releases/latest/download/agentc-linux-arm64)
· [SHA256SUMS](https://github.com/Sidd065/agent-companion/releases/latest/download/SHA256SUMS)

### 2. On the phone

You need Android 9 or newer. On the phone, open
**[agent-companion.apk](https://github.com/Sidd065/agent-companion/releases/latest/download/agent-companion.apk)**
and install it. Android asks you to allow installs from your browser the first time.

### 3. Pair

On the computer:

```sh
agentc pair
```

In the app, tap **Scan QR code** (or **Hosts → Add host**) and scan the code. Check that the fingerprint in
the app matches the one printed under the QR code. Repeat on every computer you want to use.

### Notifications

While the app is open, notifications arrive over its encrypted connection. When it's closed, you have
two options:

- **Settings → Persistent connection** keeps a background connection open. It works with any build but
  uses more battery.
- **Push notifications** through Firebase Cloud Messaging. Each computer needs the service account key of
  the Firebase project the app was built with:
  1. Save the key as `~/.config/agentc/firebase-service-account.json` (mode 600).
  2. Run `systemctl --user restart agentc`.
  3. In the app, use **Settings → Send test**.

  The released APK uses the maintainer's Firebase project, so for push on your own computers,
  [build the app](#building-from-source) with your own Firebase project.

### Upgrading

Download the new `agentc-linux-*` binary and run `install` again. It upgrades in place and keeps the tunnel
URL and paired phones. Install the new APK over the old one.

## Security

The daemon only listens on `127.0.0.1`. `cloudflared` publishes it at a random
`https://<words>.trycloudflare.com` URL. Anyone can reach that URL, so the daemon doesn't rely on keeping
it secret:

- **End-to-end encryption.** All traffic runs inside a [Noise](https://noiseprotocol.org) channel
  (`Noise_IK_25519_ChaChaPoly_SHA256`). Cloudflare only relays ciphertext; it never sees messages, file
  contents or terminal I/O.
- **Mutual authentication.** The daemon has a long-term identity key, and each phone has its own key per
  computer, wrapped by the Android Keystore. A connection succeeds only when the phone is paired and the
  daemon proves it holds its identity key. Anyone else's handshake fails.
- **One-time QR pairing.** `agentc pair` shows a QR code containing the tunnel URL, the daemon's public key
  and a one-time 256-bit secret. Pairing uses `Noise_IKpsk1`, so only someone holding the code can pair.
  The code works once, expires after 10 minutes, and is bound to the daemon key it names, which stops
  man-in-the-middle attacks. The secret is in the URL fragment, which browsers never send to servers.
- **Minimal public surface.** Every path returns 404 except the encrypted WebSocket endpoint and a pairing
  page that exists only while a code is open. Failed handshakes are throttled per client IP.
- **Device management.** Run `agentc devices` / `agentc devices revoke ID`, or use **Host → Paired devices**
  in the app. Unpairing a phone also drops its push tokens and closes its connections.
- **Push privacy.** Push messages go through Google's FCM. With `"pushContent": "minimal"` they carry no
  conversation text.

## The tunnel URL

A quick tunnel's address is random and changes when `cloudflared` restarts, for example after a reboot.
agentc adopts its running cloudflared again when the daemon restarts, so upgrades and
`systemctl --user restart agentc` keep the URL. When the URL does change, the daemon pushes the new address
to paired phones (this needs push notifications), and you can always re-scan `agentc pair` on a paired phone
to update it.

To use a stable URL instead, such as a named Cloudflare tunnel, a reverse proxy or a LAN address, set
`"publicUrl"` and `"tunnel": "none"`. To pair over a LAN, run `agentc pair --url http://192.168.1.5:9473`.
Plain `http://` is still end-to-end encrypted.

## Commands

```
agentc install [--port N]      install the binary, claudr/agyr and the systemd user service
agentc pair [--url URL]        QR code for pairing a phone (one-time, 10 minutes)
agentc status                  daemon, tunnel URL, sessions, push and paired devices
agentc devices [revoke ID]     list or unpair devices
agentc tunnel stop             stop the tunnel (a new one, with a new URL, starts within ~15 s)
agentc term ls|attach|kill     shared terminals
claudr [claude args…]          Claude Code in a shared terminal the phone can see (--safe drops skip-permissions)
agyr [agy args…]               agy in a shared terminal the phone can see (--safe drops skip-permissions)
```

## Configuration

`~/.config/agentc/config.json`:

| Key | Default | Meaning |
|---|---|---|
| `port` | `9473` | Local port (`127.0.0.1:9473`). The tunnel metrics port is `port+1`. |
| `listen` | `127.0.0.1:<port>` | Listen address |
| `tunnel` | `"quick"` | `"quick"` runs a Cloudflare quick tunnel; `"none"` uses `publicUrl` |
| `publicUrl` | — | The URL to advertise when you bring your own tunnel or proxy |
| `cloudflared` | `cloudflared` | Path to the cloudflared binary |
| `idleHibernateMinutes` | `30` | Stop an idle session's agent process; it resumes on the next message |
| `maxLive` | `8` | Maximum number of running agent processes |
| `defaultSkipPermissions` | `true` | Used only when a client doesn't send its own flags |
| `claudePath` | `~/.local/bin/claude` or `claude` on `PATH` | The Claude Code executable |
| `claudrArgs` | `["--dangerously-skip-permissions"]` | Default flags for `claudr` |
| `agyrArgs` | `["--dangerously-skip-permissions"]` | Default flags for `agyr` |
| `pushContent` | `"full"` | `"minimal"` keeps conversation text out of push messages |
| `firebaseServiceAccount` | `~/.config/agentc/firebase-service-account.json` | FCM service account key |

State lives in `~/.local/state/agentc/`: the host key (`identity.json`), paired devices (`devices.json`),
logs, and per-session agent logs.

## Building from source

You need Go (see `daemon/go.mod`), JDK 17 and the Android SDK.

```sh
cd daemon && go build -o agentc . && ./agentc install
cd android && ./gradlew assembleRelease   # app/build/outputs/apk/release/app-release.apk
```

Release APKs are signed with the build machine's debug key, which is fine for sideloading. To ship under
your own key, set a release `signingConfig`.

**Your own Firebase project (for push):**

1. Create a Firebase project with an Android app for the package `io.github.sidd065.agentcompanion`.
2. Replace `android/app/google-services.json` with that project's file.
3. Run `python3 android/tools/gen_google_services.py`.
4. Build the app.
5. Save the project's service account key (*Project settings → Service accounts → Generate new private
   key*) on each computer as described under [Notifications](#notifications).

### Tests

```sh
cd daemon
go vet ./... && go test ./...                       # includes Noise interop vectors and pairing tests
go run ./cmd/agentctl pair --local                  # pair a CLI client through the local socket
go run ./cmd/agentctl itest                         # end-to-end agy scenarios against a running daemon
go run ./cmd/agentctl itest claude                  # end-to-end Claude Code scenarios (real turns on Haiku)
CLAUDE_ITEST=1 go test ./internal/claude -run Live  # the Claude adapter against a real claude

cd android
./gradlew testDebugUnitTest                         # NoiseTest checks the Kotlin client against the Go vectors
```

### Layout

- `daemon/internal/`:
  - `secure`: Noise transport, pairing and the device registry.
  - `tunnel`: cloudflared.
  - `server`: RPC and terminals over the secure channel, and the pairing page.
  - `core`: sessions, transcripts, live tasks, subagents, artifacts and comments.
  - `claude`: the Claude Code stream-json driver behind an in-process ACP adapter, plus history, replay and
    checkpoints.
  - `acp`, `agyproc` and `lsapi`: agy.
  - `term`: tmux.
  - `fsops`, `push` (FCM) and `qrterm`.
- `android/app`:
  - `net/`: the Noise client, secure socket and RPC.
  - `core/`: models, pairing and Keystore-wrapped keys.
  - `ui/`: Compose screens.
- `android/terminal`: Termux's `terminal-emulator` and `terminal-view` (Apache License 2.0), adapted to a
  remote PTY.

## Notes

Agent Companion is an independent project and is not affiliated with Anthropic or Google. Claude Code is
Anthropic's; agy (Antigravity) is Google's.
