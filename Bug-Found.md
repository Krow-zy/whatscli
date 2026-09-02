# Bug Found

Bugs in the message-transit path (what actually crosses the wire to the other
user), not app infrastructure. Verified by reading the vendored whatsmeow
source (`go.mau.fi/whatsmeow@v0.0.0-20260730092514-662ad1dc6900`).

---

## Bug 1 — Revoke sends a wrong wire key, then falsifies the local transcript

**Location:** `messages/session_manager.go` `revokeMessage()` (~line 668)

**What happens:**

1. Select a message **from the other person**, press `r` (revoke).
2. whatscli calls `client.RevokeMessage(ctx, chat, id)` for any message, with
   no `msg.FromMe` check.
3. whatsmeow's `RevokeMessage` (send.go:488) is
   `SendMessage(ctx, chat, BuildRevoke(chat, types.EmptyJID, id))`, and
   `BuildMessageKey` with an empty sender hardcodes `FromMe: true` with no
   `Participant` (send.go:494-506).
4. The wire revoke therefore claims *"my own message with that ID"* — which
   does not exist. The server ACKs the node (no error), recipients silently
   ignore it, and **the message stays alive for everyone else**.
5. whatscli then runs `db.MarkMessageRevoked(msg.Id)` unconditionally, so your
   screen shows `[message revoked]` — a lie about the shared conversation
   state.

**Corollary:** the legitimate case — a group admin revoking a member's
message — is impossible, because that requires
`BuildRevoke(chat, senderJID, id)` with the real sender, which whatscli never
passes.

**Fix direction:** in `revokeMessage`: own message → keep current path;
other's message in 1:1 → reject ("you can only revoke your own messages");
other's message in a group → parse `msg.SenderId` and send
`client.SendMessage(ctx, chatJID, client.BuildRevoke(chatJID, sender, id))`.

---

## Bug 2 — Sent media is malformed: kind is never checked against the file,
and required metadata is missing (can lag/crash the receiver's app)

**Location:** `messages/session_manager.go` `sendMediaCommand()` (~line 655)
and `sendMedia()` (~line 857)

**What happens:**

- `/sendimage`, `/sendvideo`, `/sendaudio` take **any file path**. Nothing
  validates that the file content matches the declared message kind:
  - `/sendimage song.mp3` → an `ImageMessage` with `Mimetype: audio/mpeg`
    and MP3 bytes.
  - `/sendaudio photo.png` → an `AudioMessage` with `Mimetype: image/png`.
  - `/sendvideo document.pdf` → a `VideoMessage` with `application/pdf`.
- The built proto messages also **always omit** fields real clients always
  send: `Width`/`Height` and `JpegThumbnail` for images, `Seconds` (duration)
  for audio/video, waveform for voice-capable audio.

**Why it matters (receiver side):**

- Official WhatsApp clients never emit such messages. The receiver's app
  trusts the declared type and decodes inline: an `ImageMessage` goes to the
  native image decoder, a `VideoMessage`/`AudioMessage` to the media player
  for duration/thumbnail extraction.
- Feeding an MP3 to an image pipeline (or a PDF to the video pipeline) is
  exactly the class of input that makes decoders hang (lag) or crash on
  malformed streams. Even in the best case the receiver gets an eternally
  "processing" attachment or a 0:00 audio that won't play.
- The mismatch is invisible to the sender: whatscli reports success and shows
  `[IMAGE]`/`[AUDIO]` locally as if everything were fine.

**Repro:** in any chat, run `/sendimage <any non-image file>` (e.g. an MP3).
The message is accepted and delivered.

**Fix direction:** in `sendMedia`, after `readUploadFile`, reject the upload
when `mimeType`'s top-level type doesn't match `kind`
(`image/*` for image, `video/*` for video, `audio/*` for audio; document
accepts anything). Optionally fill `Seconds`/`Width`/`Height` where cheap to
obtain.

## Experiment log — receiver lag/crash hypothesis: FALSIFIED on modern clients

Tested against WhatsApp (physical phone, 8 GB RAM), payloads generated in
`payloads/` (gitignored):

| Payload | Result on receiver |
|---|---|
| `/sendimage` with MP3 (kind confusion) | Delivered, benign — defensive media parser |
| Zalgo text, 4000 combining marks | Collapsed behind "Read more"; when expanded renders stacked, zero lag |
| Pixel bomb PNG 50000×50000 (~9.5 GB decoded), no `JpegThumbnail` | Loaded and displayed as white image — defensive subsampling |

Conclusion: current WhatsApp builds defuse every payload class whatscli can
emit (text collapse + robust shaping, image subsampling, no inline document
decode). Bug 2's real impact is downgraded to: broken previews, missing
duration/dimension metadata, and unsanitized passthrough. The fix stays
worth doing as a robustness guard, not as a crash fix.

## Incident analysis — WhatsApp Business freezes on device A (2026-08-07)

Unrelated to the payloads. Forensics via adb dropbox on device A:

- 14 × `data_app_anr` for `com.whatsapp.w4b` v**2.26.31.70**, all on
  2026-08-07 (00:10–16:16): "Input dispatching timed out —
  com.whatsapp.home.ui.HomeActivity is not responding", first one
  "No response to onStopJob".
- No `data_app_crash` entries for w4b at all (no Java/native crash).
- During freezes: ~0% CPU for w4b, memory pressure ~0 (`/proc/pressure/memory`
  avg10=0.20, RSS ~255 MB) → main-thread **deadlock**, not decode/OOM.
- Process so wedged tombstoned couldn't dump it (ptrace_stop on all threads).
- App auto-updated to **2.26.31.72** on 2026-08-08 01:07:50
  (`lastUpdateTime`) → zero ANRs since.

Verdict: bad-build hang in WhatsApp Business 2.26.31.70, cured by the
2.26.31.72 hotfix. The payload timing (sent 2–3 days prior, zero ANRs in
between) and the quiet CPU/memory during freezes exonerate our payloads as
the mechanism.

---

## Secondary transit findings (not fixed, noted for later)

- **Edits dropped:** whatsmeow unwraps edited messages and sets
  `evt.IsEdit = true` (`types/events/events.go:412-414`, auto-run at
  `client.go:998`). `normalizeEventMessage` never checks `IsEdit`, so an edit
  from the other user never updates the transcript — the stale original text
  shows forever. Needs a text-update path in storage.
- **`GroupMentionedMessage` dropped:** that wrapper is unwrapped by neither
  whatsmeow's `UnwrapRaw` nor whatscli's `unwrapMessage`, so group messages
  using it fall through to "ignore" (legacy wrapper, rare in current clients).

**Ruled out as non-bugs:** sending to `@lid` chats (handled at
whatsmeow send.go:323), `DocumentWithCaptionMessage` (pre-unwrapped by
whatsmeow), read-receipt batching, MIME sniffing itself.
