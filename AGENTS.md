# AGENTS.md

Guidance for AI coding agents working in this repository. Assume the reader knows nothing about the project.

## Project overview

**whatscli** is a terminal UI (TUI) WhatsApp client written in Go. It connects to WhatsApp through the Web App API (no browser) using the [`go.mau.fi/whatsmeow`](https://github.com/tulir/whatsmeow) library and renders its interface with [`tview`](https://github.com/rivo/tview) / [`tcell`](https://github.com/gdamore/tcell). Users log in once by scanning a QR code, then send/receive text and media messages, manage basic group membership, and receive desktop notifications — all from the terminal.

This repository is a **customized fork of [normen/whatscli](https://github.com/normen/whatscli)** (upstream v1.1.5) with a reworked "warm retro" theme, an activity-filtered chat sidebar split into Contacts/Groups sections, in-memory instant search, desktop notifications, per-chat online presence, and focus indicators. The fork's delta is documented at the top of `README.md` under "Changes in this fork".

Key facts:

- Go module: `github.com/normen/whatscli` (see `go.mod`), Go **1.25.0**.
- Current version string: `var VERSION string` in `main.go` (single source of truth for releases; `release.sh` parses it from there).
- License: MIT (`LICENSE`).
- Development happens on Windows (Git Bash), but the app targets Linux, macOS, Windows, and Raspberry Pi (ARM).

### Main dependencies

| Dependency | Purpose |
|---|---|
| `go.mau.fi/whatsmeow` | WhatsApp multidevice protocol: websocket connection, session store, media upload/download, protobuf message types |
| `github.com/rivo/tview` + `github.com/gdamore/tcell/v2` | TUI widgets, layout, key events |
| `code.rocketnine.space/tslocum/cbind` | User-configurable key bindings |
| `gopkg.in/ini.v1` | INI config file parsing (`whatscli.config`) |
| `github.com/adrg/xdg` | XDG base-directory paths for config/session files |
| `modernc.org/sqlite` | Pure-Go SQLite driver for whatsmeow's session store |
| `github.com/gen2brain/beeep` | Desktop notifications (Windows toast etc.) |
| `github.com/zyedidia/clipboard` | Clipboard copy/paste of user IDs |
| `github.com/skip2/go-qrcode` + local `qrcode` package | ANSI QR-code rendering for login |

## Architecture and runtime model

Three packages, all `main`-package free:

```
main.go              UI: tview grid layout, key bindings, command input, help screens
config/              Singleton config struct, INI load/save, theme definitions
messages/            Core logic: SessionManager (network + commands), MessageDatabase (in-memory store)
qrcode/              Vendored BSD-licensed terminal QR-code renderer (3-Clause BSD header; do not reformat)
```

### Threading model (important)

- The **tview app runs on the main goroutine**. All UI mutations from other goroutines must go through `app.QueueUpdateDraw`.
- `messages/session_manager.go` starts `runManager()` in a **separate goroutine**. It owns the whatsmeow client and drains channels (`CommandChannel`, `StatusChannel`, `BatteryChannel`) in a select loop.
- The UI never touches whatsmeow directly; it pushes `messages.Command{Name, Params}` values into `sessionManager.CommandChannel` and receives results through the `messages.UiMessageHandler` interface (`messages/messages.go`), implemented by `UiHandler` in `main.go`.
- `MessageDatabase` (`messages/storage.go`) is an **in-memory, mutex-guarded** store of messages/chats/contacts — persistence of *messages* is only what WhatsApp's history sync delivers; only the *session* (login credentials) is persisted, in a whatsmeow SQLite store at `config.GetSessionFilePath() + ".db"`.
- whatsmeow events arrive in `eventHandler.Handle` (`session_manager.go`), which normalizes them into the internal `Message`/`Chat` structs and calls the UI handler. `unwrapMessage` peels ephemeral/view-once/device-sent wrappers before inspection.

### Data structures

- `messages.Message` — internal message representation abstracting the protobuf (`RawMessage *waProto.Message` retained for downloads). `Kind` is one of `MessageKindText/Image/Video/Audio/Document/Unknown`.
- `messages.Chat` — sidebar entry; `Id` is a JID string. Suffix conventions: `@g.us` = group (`GROUPSUFFIX`), `@s.whatsapp.net` = 1:1 contact (`CONTACTSUFFIX`), `status@broadcast` = never displayed (`STATUSSUFFIX`).
- `Chat.IsMuted()` interprets `MutedUntil`: `0` = not muted, `-1` = muted forever, `> now` = mute expiry.

### Commands

Slash-commands typed in the input field (`EnterCommand` in `main.go`) or global key bindings are converted to `Command` values and dispatched in `SessionManager.execCommand` (a big `switch`). To add a command: handle it in `execCommand`, print usage via `printCommandUsage`, and (if user-visible) list it in `PrintCommands` in `main.go`.

### Configuration

- Singleton in `config/settings.go`: `config.Config` (`IniFile` struct embedding `General`, `Keymap`, `Ui`, `Colors` with defaults inline).
- Loaded from `xdg.ConfigFile("whatscli/whatscli.config")` (Windows: `%LOCALAPPDATA%\whatscli\whatscli.config` — adrg/xdg maps configHome to LocalAppData on Windows, verified in `paths_windows.go`; session DBs live in the same dir). Sections: `[general]`, `[keymap]`, `[ui]`, `[colors]`; keys use `TitleUnderscore` mapping (`EnableNotifications` → `enable_notifications`). Missing file → defaults written back out.
- Themes live in `config/theme.go` (`RetroTheme()` = "warm retro"). `ApplyTheme` copies a `Theme` into `Config.Colors`. Only theme currently: `"warm"`. Config `[colors]` section can override individual roles afterward.
- Color roles are referenced everywhere as `config.Config.Colors.<Role>` and rendered with tview dynamic-color markup strings like `"[" + config.Config.Colors.Positive + "]online[-]"`.
- Diagnostics fields (`EnableDiagnostics`, `DebugInputEvents`, `DebugEventFlow`, `DebugUiUpdates`, `DiagnosticsLogPath`) exist in `General` with `false` defaults but are **not consumed anywhere** yet — safe to ignore, and don't assume they do something.
- **Multi-profile** (`Profile` in `[general]`): per-account session DBs (`session.db` default, `session.<name>.db` otherwise). Switch via `--profile <name>` flag or `/profile <name>` in-app; list via `/profile`; delete a non-active profile's local DB via `/profile remove <name>` (`removeProfile` refuses the active profile and validates the name; server-side unlink is still `/logout` while that profile is active). `switchProfile` tears down client/container via `teardownProfile` (`sm.db.Reset()`, clear `sm.currentReceiver`, `uiHandler.ResetChat()` sync `QueueUpdateDraw` to wipe main-UI state — any new per-account UI state must be reset there too, or it leaks across accounts). **The profile name is persisted to config only after a successful login** (`saveActiveProfile` in `loginWithQRCode`/`loginWithConnection`), so abandoning a new-profile QR does not strand the user on an empty profile — this was the "logged out of default" regression. `config.ProfileDbPath(name)` resolves a name to its DB path without mutating the global. Names validated by `ValidProfileName` (`^[A-Za-z0-9_-]+$`) — never bypass, the name is embedded in a file path.
- **Passphrase gate** (`EnablePassphrase`, `PassphraseHash` in `[general]`): lock screen (`makeLockView`) shown at startup only when a hash is set AND the active profile's session DB file exists (no stored session → no gate). Hash format `pbkdf2-sha256:<iter>:<salthex>:<hashhex>` via stdlib `crypto/pbkdf2` (600k iterations). Set/change/remove via `/passphrase [remove]` (`showPassphraseDialog`). The `uiGate` bool (main.go) makes the app-level input capture skip global shortcuts while lock screen or dialog is shown — without it, the login keybind (Ctrl+r) connects around the gate. Persisted via `SaveGeneralKeys`, never plain INI `ReflectFrom` (it would bake the whole theme into the file).
- **Manual-only chat history (intentional, do not "fix" it back)**: server-pushed history-sync chunks (`INITIAL_BOOTSTRAP`, `RECENT`, `FULL`) are **dropped** in `handleHistorySync` (`messages/session_manager.go`) — only `ON_DEMAND` chunks (answers to `/backlog` requests) are ingested. A chat transcript being empty at session start is the designed behavior, not a bug; the sidebar still gets chat metadata (names, unread, mute) via `importChatMetadata`. `/backlog [minutes]` fetches in 50-message batches until the requested window is covered, then trims older messages; `history_window_min` in `[general]` supplies the default window when `/backlog` is called without an argument (0 = single batch). The account picker shown after the passphrase unlock is part of `uiGate` — global shortcuts stay disabled until a profile is chosen.
- Env var `WHATSCLI_DEBUG_BG=1` swaps the main view for a color-swatch debug screen (`makeDebugBackgroundView` in `main.go`).

### Debugging rules learned the hard way (follow these)

1. **Check the live state the feature depends on before blaming code or the user.** For the passphrase gate that means: read `whatscli.config` (does `passphrase_hash` exist?) and check for `session.db` in the same dir. A missing hash means the gate has never been armed — "wrong passphrase" reports then point at dialog logic, not at the user's typing.
2. **Verify which binary the user actually runs.** Installed copy lives at `%GOPATH%\bin\whatscli.exe` (desktop shortcut target); a stale `whatscli.exe` in the repo root shadows it when launching from the repo dir (CWD precedes PATH). Compare `LastWriteTime` of the installed exe against the last `go install` before diagnosing anything.
3. **Trace UI state machines against their labels, not against intent.** The passphrase dialog bug that shipped twice: `case step == 0` ran "verify current" even when no hash existed, so the *first new passphrase entry* was verified against an empty hash and always failed with "wrong passphrase". The label said one thing, the switch did another. Rule: for every dialog step, assert which branch runs for each initial state (hash present vs absent) before building.
4. **`app.SetFocus` redirects ALL key events** (verified in tview source). After swapping the root primitive, re-point focus at the new root's input — leaving `app.SetFocus(textInput)` in place while the lock screen is root lets keystrokes reach the hidden main UI and bypass the gate entirely.
5. **INI values pass through `os.ExpandEnv` on load** (`cfg.ValueMapper`). Any value containing `$` gets env-expanded and corrupted (`$600000` → empty). Hash format uses `:` separators for this reason; never store `$`-containing values in this config.
6. **A fix is only done when the user's actual reproduction passes on the binary they launch.** `go install` + green tests are not proof; the desktop-shortcut binary must be reinstalled and its timestamp confirmed. If confused after evidence gathering, ask the user what they saw — they know their machine better than the logs do.

## Build and test commands

```bash
go build ./...        # compile everything
go test ./...         # run all tests (~7s)
go run .              # run the app (shows QR login if no session stored)
go vet ./...          # static checks
```

`Makefile` targets mirror these: `build`, `clean`, `run`, `install`, `get`, `update`, `release` (delegates to `./release.sh`).

Notes:

- Builds are plain `go build` with **`CGO_ENABLED=1`** in CI (the modernc SQLite driver is pure Go but CI keeps cgo on for consistency). Don't add cgo-dependent libraries casually.
- `whatscli.exe` in the repo root is a local build artifact; it's gitignored. Never commit binaries.
- There is **no lint config, no CI test job** — CI only builds and releases. Run `go vet` and `go test` yourself before declaring work done.

## Code style guidelines

- Standard Go formatting (`gofmt`/`goimports`); exported symbols get short doc comments starting with the symbol name (see `storage.go`); unexported helpers often get a `//` line only when non-obvious.
- Errors are created with `fmt.Errorf("context: %v", err)` and surfaced to the user through `sm.uiHandler.PrintError(err)` / `PrintError` in `main.go` — errors are user-visible terminal output, never panics or logs.
- All user-facing text goes to the tview text view via `fmt.Fprintln(textView, ...)` with tview color markup; use the color-role config values, not hardcoded colors.
- Command/input validation: `checkParam(params, n)` guards command arity; JIDs are always parsed with `types.ParseJID` and errors surfaced.
- The codebase contains occasional `ponytail:` comments marking deliberate simplifications — keep that convention if you add one.
- Keep whatsmeow-specific types (`types.JID`, `waProto.*`) out of `main.go`; the `messages` package owns the abstraction boundary.

## Testing instructions and conventions

Tests are plain stdlib `testing` (no testify, no testdata fixtures). Run with `go test ./...`.

Existing suites and what they cover:

- `config/settings_test.go` — default config/theme values (acts as a regression pin on the palette hexes).
- `main_nav_test.go` — sidebar wrap navigation and help-toggle restore, tested by constructing real `tview` widgets and assigning the package-global vars (`chatRoot`, `treeView`, `textView`…). This is the established pattern for UI tests: no driver, just build the primitive and call the handler.
- `messages/storage_test.go` — `MessageDatabase` add/read/unread/mark-read semantics.
- `messages/chat_sort_test.go`, `group_name_test.go` — chat ordering; group chats must not take a sender's name as title.
- `messages/search_test.go`, `session_manager_test.go` — search matching; sidebar filtering (status/newsletters never shown, `recency_only`); mute-state merging; **download filename sanitization** (path traversal like `../../.ssh/authorized_keys` and `..\..\...` must reduce to basename).
- `messages/unwrap_selfcheck_test.go` — a `func init()` self-check that panics if `unwrapMessage` stops unwrapping nested ephemeral/view-once/device-sent wrappers.

Conventions when adding tests:

- Pure functions and `MessageDatabase`/`SessionManager` logic are tested without network, QR, or whatsmeow client — construct the struct and set `sm.db` directly.
- Message-kind confusion and hostile-file-name cases are security-relevant: cover them like `TestDownloadFileNameSanitizesPathTraversal` does.

## Security considerations

- **Login credentials** are stored by whatsmeow in a SQLite DB next to the config dir (`whatscli/session.db`). `/logout` removes the server-side link; `/reset` deletes the local DB file. Never log, copy, or transmit these files.
- The app processes attacker-controlled content (message text, file names, MIME types, URLs). Maintain the existing guards: `downloadFileName` basename-sanitizes traversal; `getTextMessageString` passes text through `tview.Escape` before color markup; `openMessageURL` only opens `http(s)` URLs matched by `urlPattern`.
- `payloads/` (gitignored) contains **security research artifacts** for this fork: fuzz corpus, a pixel-bomb PNG, zalgo text, and `fuzz_send.go` — a standalone `//go:build ignore` program that pairs as a companion device and sends the corpus to a chat. It is explicitly "ONLY use against your own accounts/devices". Do not wire any of this into the app, do not commit payloads, and never run it against third parties.
- No message automation by design — there is intentionally no CLI/shell interface for sending messages (stated in `README.md` caveats). Don't add one.

## Known bugs and open issues

Read `Bug-Found.md` before touching the message-transit path. Documented, unfixed:

1. **Revoke** (`revokeMessage`): revoking *another user's* message sends a wrong wire key (whatsmeow hardcodes `FromMe: true` without participant), so the revoke is silently ignored by recipients while the local transcript falsely shows `[message revoked]`. Fix direction is sketched in the file.
2. **Media kind confusion** (`sendMedia`): `/sendimage` etc. never validate the file's MIME type against the declared message kind, and omit `Width`/`Height`/`Seconds`/thumbnail metadata. Impact downgraded by testing (modern clients are defensive) but the guard is still wanted.
3. **Edits dropped**: `evt.IsEdit` from whatsmeow is never handled, so incoming edits never update the transcript.

## Release / deployment process

Releasing is fully automated from a tag push; the release script requires `git` and the GitHub CLI (`gh`) authenticated.

```bash
./release.sh            # reads VERSION from main.go, tags, pushes, watches the workflow
./release.sh v1.2.1     # explicit version
./release.sh --no-watch # don't block on the workflow run
```

`./.github/workflows/release.yml` then:

1. Builds native binaries on matrix runners: `linux/amd64` and `windows/amd64` (with `CGO_ENABLED=1`), plus `linux/arm` (GOARM=5, Raspberry Pi) cross-compiled on Ubuntu with `gcc-arm-linux-gnueabi`.
2. Zips each binary as `whatscli-<version>-{linux,windows,raspberrypi}.zip`, generates release notes from `git log` since the previous tag, and publishes the GitHub release via `softprops/action-gh-release`.
3. Updates the **Homebrew tap** repo `normen/homebrew-tap` (formula sha256/url, using secret `HOMEBREW_TAP_TOKEN`).
4. Generates AUR `PKGBUILD`s via `./.github/scripts/generate-aur-pkgbuilds.sh` and publishes both `whatscli` and `whatscli-git` to the **AUR** using `AUR_USERNAME`/`AUR_EMAIL`/`AUR_SSH_PRIVATE_KEY` secrets (generated files land in `./.github/aur/`).

To cut a release: bump `VERSION` in `main.go`, commit, then run `./release.sh`. If a tag already exists on origin, the script re-triggers the workflow via `workflow_dispatch` instead of re-tagging.

## Repo layout notes

- `doc/screenshot.png` — README screenshot.
- `graphify-out/` (gitignored) — output of an external code-graph tool; ignore it.
- `.github/FUNDING.yml`, `.github/PULL_REQUEST_TEMPLATE.md` — upstream remnants; the PR template references the old go-whatsapp→whatsmeow migration, not current practice.
- `.codex` — empty marker file; ignore.
