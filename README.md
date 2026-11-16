# Agent Companion

Run and follow coding agents on your computers from an Android phone, like the Claude app's remote control
for Claude Code. It supports **Claude Code** and **agy** (Google's Antigravity CLI); Codex is next.

```
 Android app ──── wss:// (Cloudflare quick tunnel) ────▶ cloudflared ─▶ agentc ──┬─ claude -p     (one per Claude session, stream-json)
  (Kotlin/Compose)   end-to-end encrypted (Noise IK)                   (Go)     ├─ agy acp       (one per agy session)
        ▲                                                                       ├─ agy language server (tasks, artifacts…)
        │                                                                       ├─ ~/.claude      (history, checkpoints, subagents)
        └────────────── Firebase Cloud Messaging (notifications) ◀──────────────┴─ tmux          (terminals: claude, agy, shells)
```

One app talks to any number of computers at once. Each runs the `agentc` daemon. Sessions and terminals
from every host show up together.

## What you can do from the phone

For both agents:

- **Chat sessions:** start an agent in any folder on any of your computers. Send messages with photos and
  files, and stop a turn. Queue follow-ups, or send them while the agent works. Switch the model, effort
  and mode, and run slash commands.
- **Live transcript:** streaming answers and thinking, tool calls with diffs, plans and to-dos, usage.
- **Approvals:** allow or deny tool calls (or *deny with a comment*), answer questions, approve plans. All
  of these also work from notifications.
- **Ongoing work:** background shell commands with **live output** and a Stop button. **Subagents**, with
  their transcript as they work and a Stop button. Messages from agents and finished tasks.
- **Artifacts and plans:** rendered, with **comments** on any paragraph, step or code block, the way you
  comment in the Claude app. You can also comment on single lines in the diff viewer.
- **History:** every conversation on each computer. Fork, rewind (with a preview of what it undoes),
  rename, pin, archive, delete, export, and per-file diffs.
- **Files:** browse, create, rename, move, copy, delete, upload, download and edit files on any host.
- **Terminals:** a full terminal emulator for shells, commands and the agents' own TUIs, with "agent needs
  you" status. Terminals keep running when you leave, and the laptop can attach to the same session
  (`claudr`, `agyr`).
- **Launch flags per agent:** every flag (`--dangerously-skip-permissions`, model, effort, permission mode,
  agent, extra folders, …) in a modal. The choice is saved on the phone and reused for new sessions and
  terminals.

Claude Code specifics:

- **Permissions the way Claude asks:** Claude's own options, including *always allow* rules
  (e.g. `Bash(npm test:*)` for this project), and the reason Claude gives for asking. A comment on a denial
  goes back to Claude as your instruction.
- **Questions** (AskUserQuestion) on a card with options, multi-select and *Other*. **Plans**
  (ExitPlanMode) on a card: approve with auto-accepted or manual edits, send back with feedback, or open
  the plan file and comment on it.
- **Steering:** messages sent while Claude works are delivered mid-turn, as in Claude Code's terminal.
- **Modes:** ask before edits, accept edits, plan, auto, don't ask, bypass permissions. Effort from low to
  max, every model Claude Code lists, cost per turn, a context meter and the full **context breakdown**
  (`/context`).
- **Side questions** (`/btw`): ask about the conversation without adding to it. **MCP servers:** status,
  enable/disable, reconnect.
- **Rewind** restores files from Claude's checkpoints as well as the conversation. **Fork** branches at any
  message. Background agents that report back after the turn ended show up as new turns.
- **Your terminal sessions too:** conversations you started in `claude` on the laptop are in the history.
  While one is open in a terminal it shows as *open in terminal* and is read-only on the phone (fork it to
  continue), so two processes never write the same conversation. Sessions started from the phone appear
  in `claude --resume` on the laptop.
- **Host screen:** Claude Code version and plan, **plan usage limits** (5-hour and weekly), running Claude
  terminals, the default model, agents, MCP servers, settings.json and `claude update`.

## Security

The daemon only listens on `127.0.0.1`. `cloudflared` publishes it at a random
`https://<words>.trycloudflare.com` URL. That URL is public, so the daemon doesn't rely on it being secret:

- **End-to-end encryption.** All traffic runs inside a [Noise](https://noiseprotocol.org) channel:
  `Noise_IK_25519_ChaChaPoly_SHA256`, with X25519 keys and ChaCha20-Poly1305. Cloudflare only relays
  ciphertext. Messages, file contents and terminal I/O are never visible to it.
- **Mutual authentication.** The daemon has a long-term identity key. Each phone gets its own key per host;
  the private key is wrapped by the Android Keystore. A connection only succeeds when the phone's key is
  paired and the daemon proves it holds its identity key. Anyone else who opens the URL gets nothing; their
  handshake fails.
- **One-time QR pairing.** `agentc pair` shows a QR code containing the tunnel URL, the daemon's public key
  and a one-time 256-bit secret. Pairing uses `Noise_IKpsk1`, so only someone holding the code can pair. The
  code works once, expires after 10 minutes, and is checked against the daemon key it names, which stops
  man-in-the-middle attacks. The secret sits in the URL fragment, which browsers never send to servers.
- **Minimal public surface.** Every path returns 404 except the encrypted WebSocket endpoint, plus a pairing
  page that exists only while a code is open. Failed handshakes are throttled per client IP.
- **Device management.** Use `agentc devices` / `agentc devices revoke ID`, or **Host → Paired devices** in
  the app. Unpairing a phone also drops its push tokens and closes its connections.
- **Push privacy.** Push messages go through Google's FCM. With `"pushContent": "minimal"` they carry no
  conversation text.

## Install

### 1. On each computer (Linux, systemd user services)

You need [`cloudflared`](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/), `tmux`
and at least one agent: [Claude Code](https://claude.com/claude-code) (`claude`, signed in) and/or `agy`.

```sh
# from a release (linux-amd64 or linux-arm64), or build: cd daemon && go build -o agentc .
chmod +x agentc && ./agentc install   # installs ~/.local/bin/agentc + claudr/agyr, starts the systemd user service
agentc status                         # shows the tunnel URL once cloudflared is up
loginctl enable-linger "$USER"        # optional: keep running while logged out
```

### 2. Pair your phone

```sh
agentc pair
```

The app opens to **Scan QR code** (or **Hosts → Add host**). Scan the code and check that the fingerprint
matches the one printed under the QR code.

If the phone doesn't have the app yet, scan the code with the camera instead. The pairing page offers the
APK download (the daemon serves `~/.local/share/agentc/agent-companion.apk`) and an *Open in app* button.

Repeat on every computer you want to control.

### 3. Notifications (optional)

The app is already registered with Firebase. To send pushes, each daemon needs the Firebase service
account key. In the Firebase console, open *Project settings → Service accounts → Generate new private key*.
Save the file as `~/.config/agentc/firebase-service-account.json` (mode 600), then run
`systemctl --user restart agentc` and use **Settings → Send test**. Without a key, use
**Settings → Persistent connection** instead.

## The tunnel URL

A quick tunnel's address is random and changes whenever `cloudflared` restarts, for example after a reboot.
agentc runs cloudflared in its own process group and adopts it again when the daemon restarts, so upgrades
and `systemctl --user restart agentc` keep the same URL. When the URL does change, the daemon pushes the new
address to paired phones (this needs the Firebase key), and the app follows it silently. You can always
re-scan `agentc pair` on a paired phone; it only updates the address.

To use a stable URL of your own instead, such as a named Cloudflare tunnel, a reverse proxy or a LAN address,
set `"publicUrl"` and `"tunnel": "none"`. Run `agentc pair --url http://192.168.1.5:9473` to pair over a LAN.
Plain `http://` is still end-to-end encrypted.

## CLI

```
agentc install [--port N]      install binary, agyr link and the systemd user service (migrates agyc installs)
agentc pair [--url URL]        QR code for pairing a phone (one-time, 10 min)
agentc status                  daemon, tunnel URL, sessions, push, paired devices
agentc devices [revoke ID]     list or unpair devices
agentc tunnel stop             stop the tunnel (a new one, with a new URL, starts within ~15 s)
agentc term ls|attach|kill     shared terminals
claudr [claude args…]          run Claude Code in a shared terminal the phone can see (adds "claudrArgs"; --safe drops skip-permissions)
agyr [agy args…]               run agy in a shared terminal the phone can see (adds "agyrArgs"; --safe drops skip-permissions)
```

## Configuration: `~/.config/agentc/config.json`

| Key | Default | Meaning |
|---|---|---|
| `port` | `9473` | Local port (`127.0.0.1:9473`). The tunnel metrics port is `port+1`. |
| `listen` | `127.0.0.1:<port>` | Listen address |
| `tunnel` | `"quick"` | `"quick"` runs a Cloudflare quick tunnel; `"none"` uses `publicUrl` instead |
| `publicUrl` | — | Advertised URL when you bring your own tunnel or proxy |
| `cloudflared` | `cloudflared` | Path to the cloudflared binary |
| `idleHibernateMinutes` | `30` | Stop an idle session's agent process (`claude -p` or `agy acp`); it resumes on the next message |
| `maxLive` | `8` | Maximum number of running agent processes |
| `defaultSkipPermissions` | `true` | Used only when a client doesn't send its own flags |
| `agyrArgs` | `["--dangerously-skip-permissions"]` | Default flags for `agyr` |
| `claudePath` | `~/.local/bin/claude` or `claude` on PATH | The Claude Code executable |
| `claudrArgs` | `["--dangerously-skip-permissions"]` | Default flags for `claudr` |
| `pushContent` | `"full"` | `"minimal"` keeps conversation text out of push messages |
| `firebaseServiceAccount` | `~/.config/agentc/firebase-service-account.json` | FCM service account key |

State lives in `~/.local/state/agentc/`:
- `identity.json`: the host key
- `devices.json`: paired devices
- `agentc.log` and `cloudflared.log`
- per-session agent logs
- `claude-annotations.json`: which Claude conversations are pinned (archive and titles live in `~/.claude`)

## Development

```sh
cd daemon
go vet ./... && go test ./...                    # includes Noise interop vectors and pairing/auth tests
go run ./cmd/agentctl pair --local               # pair a CLI client through the local socket
go run ./cmd/agentctl itest                      # end-to-end scenarios against a running daemon (real agy turns)
go run ./cmd/agentctl itest claude               # the Claude Code scenarios (real turns on haiku)
CLAUDE_ITEST=1 go test ./internal/claude -run Live  # the Claude adapter against a real claude
GEN_NOISE_VECTORS=../android/app/src/test/resources/noise-vectors.json go test ./internal/secure -run Vectors

cd android
./gradlew testDebugUnitTest assembleRelease      # NoiseTest checks the Kotlin client byte-for-byte against the Go vectors
```

Release APKs are signed with the build machine's debug keystore, which is fine for sideloading. To ship
under your own key, set a release `signingConfig`.

Layout:
- `daemon/`: `internal/secure` (Noise transport, pairing, device registry), `tunnel` (cloudflared), `server`
  (RPC and terminals over the secure channel, pairing page), `core` (sessions, transcripts, live
  tasks/subagents/artifacts, comments), `claude` (Claude Code: the stream-json driver behind an in-process
  ACP adapter, history, replay, checkpoints, subagents), `acp`, `agyproc`, `lsapi` (agy's language server),
  `term` (tmux), `fsops`, `push` (FCM), `qrterm`.
- `android/app`: `net/` (`Noise.kt`, `SecureSocket.kt`, RPC, image fetcher), `core/` (models, pairing,
  Keystore-wrapped keys), `ui/` (Compose screens).
- `android/terminal`: Termux `terminal-emulator` + `terminal-view` (Apache License 2.0), adapted to a
  remote PTY.
