# Architecture

This document explains how knowpod-service works inside and how to extend it. For the device
side of the upload protocol, see [Device upload protocol](device-protocol.md); for
deployment, see [Operations](operations.md).

## Components

```
                ┌──────────────── knowpod-service (one binary) ─────────────────┐
 gadget ─HTTPS─▶│ transport/http ─▶ service.UploadService ─▶ spool (local disk)  │
                │                          │                                     │
 browser ─HTTPS▶│ web UI, auth + admin     ▼ OnReceived: wake                    │
 (session)      │ handlers           worker ─▶ service.Archiver ─▶ audio (FLAC)  │
                │                                    │                           │
                └────────────┬───────────────────────┼───────────────────────────┘
                             ▼                       ▼
                         MongoDB                 S3 bucket
             (devices, recordings, users,   (recordings/…/*.flac)
                      sessions)
```

The code is layered so that the application logic depends only on interfaces:

| Package | Role |
|---|---|
| `domain/…` | Plain models (`Recording`, `Device`, `User`, `Session`) and shared errors. No I/O. |
| `ports` | Interfaces the services need: repositories for recordings, devices, users and sessions, and `ObjectStore`. |
| `service` | Application logic: web UI sign-in, device tokens, the upload protocol and its disk spool, the archive stage. |
| `worker` | Generic background pipeline: claims work from the database and runs stages. |
| `audio` | WAV header parsing and WAV → FLAC encoding. |
| `webpush` | Sends Web Push notifications: RFC 8291 payload encryption and VAPID (RFC 8292) signing, standard library only. |
| `repository/mongo`, `storage/s3` | Production implementations of the ports. |
| `repository/memory`, `storage/memory` | In-memory implementations, used by the tests. |
| `transport/http` | Router, authentication middleware, handlers. Maps errors to HTTP status codes. |
| `cmd/server` | Reads config, connects everything, starts the HTTP server and background jobs. |

## Users, ownership and authentication

Every recording and device has an `ownerId`: the user it belongs to. Users see and act only
on their own data and the notes shared with them (see **Sharing** below); the services check
this with `Account.Owns` / `Account.OwnerFilter` (`service/auth.go`) and, for notes,
`RecordingService.load` (`service/sharing.go`). Anything the user can't see answers `404`, as
if it didn't exist.

### Sharing (`service/sharing.go`, `domain/recording/share.go`)

An owner shares a note with other users as `viewer` or `editor`; everything under the note
is shared with it. The note's `shares` hold its own shares; its `members` are everyone it is
shared with: its own shares merged with the members of the note it is under (the higher role
wins), computed by `recording.ComputeMembers`. `syncMembers` recomputes them down the
sub-notes whenever shares change or a note moves (`SetParent`, `SetFolder`, trash, restore,
delete). Lists query `members.userId`, so no walk up the tree is needed.

Each member entry also holds that member's own view of the note: the folder they put it in
(only for a `root` note, one shared by itself rather than through its parent), their labels,
their order and their reminder (`remindAt`, in their own time zone, sent by
`SendDueMemberReminders`). `present` shows a member the note that way; built-in labels (the
task label) stay the note's. Deleting a folder or label reaches the member entries too
(`MoveFolder`, `RemoveLabel`), and deleting a user takes them off every note (`RemoveMember`).

Viewers read; editors also change the text and task fields, and add, move (within the owner's
notes they can edit) and trash notes under the shared note (new ones belong to the owner, with
`createdBy` set). Only the owner shares, trashes the shared note itself, deletes for good,
restores and reprocesses. A member can leave a note shared with them directly.

**Shared folders** (`service/folder_sharing.go`). A folder has `shares` like a note, and is
shared with everything in it: its folders, at any depth, and their notes with the notes under
them. `folderMembers` merges the shares of the folder and the folders it is in; a note under
no other note gets them as inherited members from its folder in `syncMembers` (boards
excepted), so a note in a shared folder is shared like a sub-note of a shared note (not
`root`: the member sees it in that folder, as `present` keeps its `folderId`). Sharing,
unsharing, moving or deleting a shared folder runs `folderChanged`: `syncFolderTree` brings
the notes in the folder's tree up to date, and the users who had or have the folder get a
`reload` event. New notes in a shared folder get its members when they are made
(`CreateText`, reMarkable imports, weekly reviews). `FolderService.List` gives a member the
folders shared with them (`ListSharedWith`) and the owner's folders in them, each with the
member's `access`; a shared folder whose parent isn't shared with the member is at their top
level. Editors add notes to a shared folder (they belong to its owner, with `createdBy`
set), make folders in it (`FolderService.Create`; they belong to its owner, too) and move its
notes between the owner's folders shared with them; the folders themselves stay the owner's
to rename, move, order, delete and share. Boards stay their maker's, so new boards don't go
into folders shared with the user. Deleting a user takes them off
every folder too (`RemoveShares`).

### Concurrent changes

Every note has a `version`, counted up by each change. `RecordingRepository.Update` replaces
the note only while it is still at the version that was read, and answers `ErrChanged`
otherwise; targeted updates (`$set`, `$inc`, …) count the version up too. User actions go
through `RecordingService.change`, which applies the change again to the newer copy; the
worker and uploads use `ports.SaveProcessed`, which keeps the user's fields
(`KeepUserFields`) and tries again. Title and text edits also carry the `revision` they were
made on (`baseRevision`); when someone else edited the text since, the edit is refused with
409 `changed` and the web app asks which version stays.

### Live updates (`service/events.go`)

`NoteEvents.Watch` wraps the recording repository, so every change of a note (by people, the
workers or imports) is published to its owner and members; `GET /me/events` streams them as
server-sent events (`note` with the ID and version, `reload` for bulk changes). The web app
loads a changed note again, updates the list and the open note, and restarts the editor on
the new text unless there are unsaved changes (then it shows the conflict). Like the
notification stream, the hub lives in the process: running several server instances would
need a shared channel (e.g. MongoDB change streams) instead.

There are four kinds of callers:

| Caller | Credential | Checked by | Grants |
|---|---|---|---|
| Recorder gadget | Device token (`Authorization: Bearer kpd_…`) | `requireDevice` → `DeviceService.Authenticate` | `/uploads/*`; recordings belong to the device's owner |
| User in the web UI | Session cookie `knowpod_session` | `requireUser` → `AuthService.Authenticate` | Own devices, recordings, Pocket settings, password |
| Admin in the web UI | Session cookie, user with role `admin` | `requireAdmin` | The above for their own data, plus `/admin/*` (users, OpenRouter) |
| Script | `ADMIN_TOKEN` (`Authorization: Bearer …`) | `requireUser` / `requireAdmin` | Everything above, acting as the built-in admin, with `All` set: lists and lookups cover every user's data |
| Pocket | HMAC signature, secret of the user named by the webhook URL | `handlePocketWebhook` | Queue recordings for that user |
| AI assistant | An OAuth access token (`Authorization: Bearer kpo_…`), or the user's MCP access token (`kpm_…`) | `handleMCP` → `MCPAccessService.Authenticate` (→ `OAuthService.Authenticate` for `kpo_`) | The MCP server's tools, on that user's own data |

A bearer header on a user/admin route is always treated as a script token; otherwise the
session cookie decides.

### Users and sign-in (`service/auth.go`, `service/users.go`)

- **Built-in admin.** At startup `EnsureBuiltInAdmin` creates the `ADMIN_EMAIL` user (role
  admin, empty password hash) if missing, and `cmd/server` assigns ownerless recordings and
  devices (from before users existed) to it. With an empty hash, the password is compared
  in constant time with `ADMIN_PASSWORD`; once a password is set, only the bcrypt hash
  counts. The built-in admin can't be deleted, renamed or demoted.
- **Signing in** checks the user's bcrypt hash. Unknown emails still spend one bcrypt
  comparison, so response times don't reveal which accounts exist.
- **Sessions.** Login creates a random 256-bit token. The browser gets it in an `HttpOnly`,
  `SameSite=Strict` cookie; MongoDB stores only its SHA-256 with the user ID and expiry.
  `Authenticate` loads the user on every request, so role changes and deletions apply at
  once; a TTL index removes expired sessions. `SameSite=Strict` is the CSRF protection.
- **Password changes** (own, with the current password; or by an admin) end the user's other
  sessions.
- **User management** keeps at least one admin, forbids deleting yourself, and deletes a
  user together with their devices, recordings (via `RecordingService`, so audio in S3 goes
  too) and sessions.
- **Rate limit.** `POST /auth/login` allows 10 requests per minute per client IP.

The frontend asks `GET /api/v1/auth/me` on load (the cookie can't be read by scripts);
`RequireLogin` in `App.tsx` sends signed-out visitors to `/login` and non-admins away from
admin pages.

## Recording lifecycle

One MongoDB document in `recordings` represents both the upload session and the recording;
its `_id` is the `uploadId` the device sees.

```
  Pocket webhook,         fetch stage OK
  reMarkable pull
  (none) ─────────▶ remote ─────────────────────┐
                                                 ▼
            create                 last byte, checksum + WAV OK        archive stage OK
  (none) ──────────▶ uploading ─────────────────────────────▶ received ──────────────▶ stored
                                                                                         │ transcribe
                                                                  summarize              ▼
                                                    summarized ◀────────────────── transcribed
                      │   ▲                                      │
     checksum mismatch│   │ spool reset, device resends          │ archive failed
                      └───┘                                      │ WORKER_MAX_ATTEMPTS times
                      │                                          ▼
                      │ intact but not a supported WAV         failed
                      └─────────────────────────────────────────▶
                      │
                      │ no progress for UPLOAD_TTL
                      └──────────▶ deleted
```

| Status | Where the audio is |
|---|---|
| `remote` | Announced by a Pocket webhook, or found by a reMarkable pull; the audio or document is still there. |
| `uploading` | Partial WAV in the spool (`UPLOAD_DIR/<id>.wav`). Its file size is the upload offset. |
| `received` | Complete audio in the spool, waiting for the worker: a verified WAV upload, or a file fetched from Pocket (`<id>.download`, any format). For documents: a zip of the document's files. |
| `stored` | FLAC (and optionally the WAV) in S3. For documents: the PDF or EPUB (`file`) and the zip (`original`). The spool files are gone. Waits for transcription (documents: reading). |
| `transcribed` | Transcript stored; waits for the summary. |
| `summarized` | Title and summary stored; fully processed. |
| `failed` | Rejected WAV: nothing kept. Archive failure: the WAV stays in the spool for recovery. AI failure: audio (and transcript) are kept; the UI offers re-transcribe/re-summarize. |

**Re-transcribe** clears transcript and summary and sets the status back to `stored`;
**re-summarize** clears the summary and sets it back to `transcribed` (`service/recordings.go`).
**Delete** removes the document, its S3 objects and any spooled files.

## Upload handling (`service/uploads.go`)

- **Idempotent create.** The unique index on `(deviceId, clientId)` guarantees one
  recording per device-assigned ID. A repeated create with the same size and checksum
  returns the existing upload; different values are a conflict.
- **Offsets come from the disk.** The spool file's size is the source of truth for the
  offset, so bytes written before a connection dropped count automatically, with no extra
  bookkeeping.
- **Serialised appends.** Appends to the same upload are serialised with striped in-process
  mutexes (64 stripes keyed by upload ID). This is one reason the service runs as a single
  instance.
- **Verification on completion.** When the offset reaches the declared size, the file is
  hashed once in full and its WAV header parsed. Only then is it marked `received`, and the
  worker is woken.
- **Stale uploads.** A job in `cmd/server` runs `PurgeStale` hourly and deletes `uploading`
  recordings not updated for `UPLOAD_TTL`, along with their spooled bytes.

## Background worker (`worker/worker.go`)

The worker runs a list of **stages**. Each moves a recording from one status to the next:

```go
type Stage struct {
	Name     string
	From, To recording.Status
	Run      func(ctx context.Context, rec *recording.Recording) error
	Cleanup  func(rec *recording.Recording) // optional, after the new status is saved
}
```

The loop, per stage:

1. **Claim.** An atomic `findOneAndUpdate` picks a recording with `status == From` and
   `notBefore <= now`, sets `notBefore = now + lease` (15 min) and increments `attempts`.
   `notBefore` is both the retry delay and the lease: while a worker holds the recording,
   nobody else can claim it, and if the process dies, the lease runs out and the recording
   is claimed again.
2. **Run.** The stage can change fields on the recording.
3. **Success.** The status becomes `To`, `attempts` resets to 0, the document is saved and
   `Cleanup` runs. Cleanup only happens after the save, so a crash in between can never lose
   data. At worst the stage runs again.
4. **Failure.** `lastError` is recorded and `notBefore` moves out by an exponential backoff
   (30 s, 1 min, 2 min … up to 1 h). When `attempts` reaches `WORKER_MAX_ATTEMPTS` the
   status becomes `failed`.

The worker checks for work every `WORKER_POLL_INTERVAL` and immediately when an upload
completes. Each stage must therefore be safe to run more than once for the same recording.

### Pocket (`service/pocket.go`)

Each user has a random `pocket.webhookId`, created the first time their Pocket settings are
opened; their webhook URL is `/api/v1/webhooks/pocket/<webhookId>`. The handler looks the
user up by that ID and verifies the signature with the user's secret. Recordings get
`ownerId` = that user and `deviceId` `pocket:<userId>`, so the (device, Pocket recording ID)
key keeps deliveries idempotent per user.

The fetch stage (`remote → received`) uses the owner's API key to ask for a pre-signed download URL, streams the file
into `<id>.download` (limited to `MAX_UPLOAD_BYTES`), identifies the format from its first
bytes (`audio.Sniff`; pre-signed storage URLs often report a generic content type), and
records size, SHA-256 and media type. The Pocket API docs don't specify the field that
holds the URL, so `pocket.findURL` accepts a bare string or the usual field names; an
unrecognised response fails the stage with the response body in `lastError`.

### reMarkable (`service/remarkable.go`, `remarkable/`)

The `remarkable` package reads the reMarkable cloud the way rmapi does: pairing turns a
one-time code into a long-lived device token (`POST /token/json/2/device/new`), each use
exchanges it for a user token (`/token/json/2/user/new`), and documents are
content-addressed files below a root (`GET /sync/v4/root`, files at `/sync/v3/files/<hash>`
with the file name in `rm-filename`). The root index lists every document and folder with
the hash of its own index, which lists its files (`.metadata`, `.content`, `<page>.rm`,
`.pdf`, …). Index schemas 3 and 4 are read. The only writes are the copies of text notes
(see **Sending text notes** below).

A pull (`RemarkableService.Pull`, and `PullAll` on a ticker in `main.go`) keeps each
user's link in `tablets`: the root hash and a cache of all items (name, parent, folder,
hash and a content hash that ignores the metadata). An unchanged root ends the pull; a
changed one re-reads only items whose hash changed, eight at a time. All documents that are
not deleted, not in the trash (directly or through a folder) and not named in the link's
`ignoredNames` (compared without case and with white space collapsed; `PATCH /me/remarkable`)
are matched to notes by
(`remarkable:<userId>`, document ID): new ones are created in `remote` in the user's knowpod
folder for reMarkable (`folderId` on the link; else a top-level folder named `reMarkable`,
any case; else a new one), inside the knowpod folder mirroring their cloud folder; finished
notes whose content hash differs are queued again. Folders are mirrored (`mirror`) when
new notes are made or the root changed since the last complete placement (`mirroredHash`):
every live cloud folder gets a knowpod folder (`folders` on the link maps them; else a free
one of the same name in the same place; else a new one, numbered when the name is taken)
whose name and parent follow the cloud's, bounded by the folder depth and cycle-safe.
Finished notes in one of the import's folders move with their documents; notes in
processing are moved by a later pull, and notes the user moved elsewhere stay. Pulls and pairing for one user are serialized
by an in-process lock. Every pull makes sure the reMarkable folder exists, even for an account
without documents, and `FolderService.Delete` refuses to delete it while the user is paired
(`remarkable: true` on it in folder lists). Imported notes are read-only: `EditSummary`
refuses notes with `source: remarkable`.

**Sending text notes** (`service/remarkable_push.go`, `remarkable/upload.go`,
`remarkable/epub.go`). Text notes of the owner in the reMarkable folder or a folder inside it
(not sub-notes, not in the trash) get a copy on the tablet: an EPUB (`remarkable.NoteEPUB`,
the title as heading and the Markdown rendered as XHTML by goldmark with GFM; checklist
boxes become ☐/☑; the same note always gives the same bytes). It goes into the cloud
folder the knowpod folder mirrors (the nearest mirrored folder it is in; the top level for
the reMarkable folder and folders made in knowpod). The note's `tablet` records the
document ID and what was sent (revision, name, cloud folder); a note whose revision, title
or folder differs, or whose last send failed, is sent again into the same document: its
`.epub` and `.metadata` are replaced and its other files (what was written on it) kept, or
a new document is made when it is gone from the cloud. A note that leaves these folders or
goes into the trash has its document's parent set to `trash` (`tablet.removed`), and back
when it returns. `Session.WriteDocuments` writes all of a user's changes as one change of
the account: each file `PUT /sync/v3/files/<sha256>` (with `rm-filename` and a CRC32C
`x-goog-hash`), the documents' indexes (schema 3), the root index (the schema read), then
`PUT /sync/v3/root` with the generation read (`broadcast: true`); when another device
changed the account meanwhile (412), it is read and written again. Sends run at the start
of each pull and, through `NoteEvents.OwnerChanged` → `RemarkableService.Nudge`, 20 s after
a user's notes stopped changing (at most 2 minutes after the first change); failures are
kept in `tablet.error` and tried again. Pulls leave out documents that are a note's copy
as notes, but read what was written on them (below).

**Text size.** A new copy's `.content` gets `textScale` 0.8 (`remarkable.TextScale`; the
tablet's default 1 looks large for these notes). Copies sent before that are sent once more
(`tablet.scaled` false): `DocumentWrite.TextScale` sets the size in their existing
`.content` only while it is still 1, and changes nothing else in the file, so a size chosen
on the tablet stays. From then on the size is never touched.

**Handwriting on copies** (`service/remarkable_ink.go`). What is written on a copy is stored
by the tablet as page files (`<id>/<page>.rm`). On each pull, for every copy whose item
`ContentHash` differs from the note's `tablet.inkHash`, `pullInk` reads the item, downloads
it when it has `.rm` files and renders the strokes of the pages that have any
(`Archive.Ink`, which also takes page files the `.content` doesn't list) with
`remarkable.WritePDF` on blank pages: the note's own text is not in it. The PDF is kept as
the note's attachment *reMarkable scribbles.pdf* (`tablet.inkAttachment`), replaced in
place when the handwriting changes and removed when it was all erased. `inkHash` is set
also when there is nothing to keep, so an untouched copy costs one lookup per change.
Failures are logged and retried with the next pull.

Stages, dispatched by `source`/`type` in `main.go`:

- `remote → received` (`Fetch`): looks the document up in the current root, downloads its
  files (not thumbnails) into a zip at `<id>.download`, limited to `MAX_UPLOAD_BYTES`.
- `received → stored` (`Store`): notebooks are parsed (`.rm` versions 3, 5 and 6; the
  line items of v6's block format) and written as a vector PDF (`remarkable.WritePDF`, one
  page per notebook page, grown to fit strokes beyond the screen); PDFs and EPUBs are
  stored as they are. The zip is stored as `<id>.rmdoc`.
- `stored → transcribed` (`AIService.Transcribe` → `readDocument`): notebook pages with
  strokes are rasterized (`remarkable.RenderPNG`, anti-aliased with `x/image/vector`) and
  sent to the document model in groups of eight; PDFs are sent as file parts. The Markdown
  answer becomes the transcript. Typed text (v6 root text blocks, `remarkable.ParseText`:
  the text's CRDT sequence put in order, paragraph styles as Markdown headings, bullets and
  checkboxes) is sent as a text part before its page's image; pages with only typed text
  are taken as they are without the model (the transcript then has no `model`), unless the
  owner's language asks for a translation. The PDF shows strokes only.
- `transcribed → summarized`: the normal summary, with a prompt for notes instead of
  conversations. A summary edited by the user survives a re-import.

### The archive stage (`service/archive.go`)

`received → stored`:

### Browser uploads (`service/manual_upload.go`)

`POST /api/v1/recordings` streams the raw request body into the spool (`<id>.download`),
hashing it and keeping the first bytes to identify the format. WAV (which must parse as
integer PCM) and MP3 are accepted as audio. The recording is created directly in `received`
with `source` `upload`, the file name as title and `X-Recorded-At` as recording time, and
the worker takes it from there like a fetched Pocket file.

Photos (JPEG, PNG, WebP, GIF, up to 20 MB, the most a model takes in one piece) and PDFs
become documents (`type: document`, `source: upload`); HEIC photos are refused, since the
models can't read them. The archive stage stores the file as it is as the document's `file`
(`Archiver.storeDocument`; `pages` is 1 for a photo), and the transcription stage has the
document model read it (`AIService.readUpload`: photos with a prompt that describes a photo
without text, PDFs like reMarkable PDFs). Documents over 20 MB are stored but not read.

**Voice memos** are recorded in the web app (`lib/wavRecorder.ts`): an audio worklet
(`public/recorder-worklet.js`) hands the microphone's audio to the page, which averages it
down to 16 kHz mono 16-bit PCM (what transcription uses anyway, about 2 MB a minute) and
writes a WAV file. It is uploaded like a WAV file with `X-Recorder: 1` (`source: recorder`)
and the moments marked while recording in `X-Highlights` (offsets in ms), which become the
recording's highlights. The recorder lives in the app frame (`context/Recorder.tsx`), so a
memo keeps recording while the user moves between pages.

**Share target.** The installed app registers as a share target (`share_target` in the
manifest, `vite.config.ts`): other apps post links, text and files to `/share`. The service
worker (`public/share-sw.js`, imported into the generated one) keeps them in the cache
`knowpod-share` and opens the page `/share` (`pages/Share.tsx`), which saves text as a text
note and uploads the files as above. Markdown files, picked, dropped or shared, never reach
the upload endpoint: the app reads them (`lib/markdownFile.ts`) and creates a text note of
each, titled by its front matter `title`, its first `#` heading or its file name. A post that
reaches the server (no service worker yet)
is redirected to the page, which asks to share again.

### Archive

Objects are stored under the owner: `recordings/<ownerId>/<id>.<ext>`. Spooled files from
Pocket or the browser that aren't WAV are uploaded unchanged.
Everything else:

1. Encode `<id>.wav` to `<id>.flac` in the spool.
2. `PutObject` the FLAC to `recordings/<deviceId>/<id>.flac` (the key uses the server ID,
   not the device's `recordingId`). With `KEEP_ORIGINAL_WAV`, the WAV goes next to it.
3. Record `audio` (and `original`) on the recording.
4. Cleanup deletes both spool files.

### FLAC encoding (`audio/flac.go`)

The encoder is pure Go ([mewkiz/flac](https://github.com/mewkiz/flac)), so the image needs
no `flac` or `ffmpeg` binary. It reads the WAV in blocks of 4096 samples per channel, so
memory use doesn't depend on recording length. Output is lossless, and the FLAC stream info
carries the MD5 of the samples.

The trade-off is compression. The library only uses fixed predictors (orders 0–4) with
Rice parameters up to 14, and channels are coded independently. Files shrink less than
with the reference `flac` tool, and noisy 24-bit audio barely shrinks at all. To compress
harder, replace `audio.EncodeFLAC` with a call to the `flac` command-line tool and install
it in the runtime image. Nothing else needs to change.

## API description

`backend/api/openapi.yaml` is the single description of the HTTP API. It is embedded in the
binary (`backend/api/api.go`) and served at `/api/v1/openapi.yaml` and, converted, at
`/api/v1/openapi.json`; the web UI's Devices tab links to it. When you add or change a
route, update the spec; the route coverage test enforces it.

### AI stages (`service/ai.go`)

Transcription and summaries run in a **second worker** (see `cmd/server/main.go`), so a
long transcription never delays archiving new uploads. Both stages have an `Enabled` check:
while OpenRouter isn't configured, recordings wait in their status without using up
attempts. Saving the settings, archiving a recording and the re-process actions wake the
AI worker. The transcription stage has a 1-hour lease because long recordings take several
model calls.

- **transcribe** (`stored → transcribed`): downloads the archived audio to a temporary file.
  FLAC is decoded with `audio.SpeechChunks` (mono, averaged down to 16 kHz, 16-bit) into
  5-minute WAV pieces; other formats are sent as they are, up to 20 MB. Each piece goes to
  OpenRouter's chat completions as an `input_audio` part with a verbatim-transcription
  prompt; the texts are joined. When the owner chose an app language (`user.Language`), the
  prompt asks for the transcript in that language (translating other speech); documents are
  read the same way.
  When an administrator picks **ElevenLabs** as the transcription service
  (`settings.TranscriptionProvider`), the archived audio is instead streamed in one piece to
  ElevenLabs' speech-to-text (`elevenlabs` package, model `scribe_v2`, with diarization and
  word time stamps; files of several hours are accepted). `elevenLabsTranscript`
  (`service/ai_speech.go`) turns the words into the same `[m:ss] Speaker N: …` lines,
  numbering speakers in the order they first speak and starting a new line on every speaker
  change and, in long turns, at a sentence end after 30 s (at the latest after a minute).
  These transcripts stay in the spoken language. Documents are always read through
  OpenRouter. `POST /admin/settings/elevenlabs/test` checks a key by transcribing a second
  of silence.
- **summarize** (`transcribed → summarized`): sends the transcript with a prompt that asks
  for a JSON object with `title`, a Markdown `summary` in the chosen summary language (auto: the owner's app language, else
  the transcript's language) and
  `actionItems` (`response_format: json_object`). `parseSummary` tolerates code fences and
  surrounding text; `parseActionItems` keeps items with text, drops due dates that aren't
  `YYYY-MM-DD` and gives each an ID. Empty transcripts get "No speech detected" without a
  model call.

The AI settings (OpenRouter key and models, transcription service, ElevenLabs key) live in the `settings` collection and are read on
every stage run, so changes apply immediately.

### Time stamps and highlights

The transcription prompt asks for a `[m:ss]` time stamp at every speaker turn (and at least
every 30 s). Long recordings are transcribed in 5-minute pieces whose stamps start at 0:00,
so `shiftTimestamps` adds each piece's start before the pieces are joined; the transcript's
stamps are relative to the whole recording.

Highlights (`recording.highlights`: `offsetMs`, optional `at`) come from the device: in the
create-upload request or via `PUT /uploads/{id}/highlights` (`service/highlights.go`
validates, converts `at` using `recordedAt`, sorts and removes duplicates). When a recording
has highlights, `summarySystemPrompt` lists their times and asks for a final "Highlights"
section that describes each moment using the transcript's stamps.

**Text notes.** Besides audio recordings, a note can be a Markdown text written in the web
app (`recording.type: "text"`). `POST /recordings/text` (`RecordingService.CreateText`)
stores its title and text as the note's `summary` with status `summarized`, so the worker
never picks it up, and the summary machinery serves it as it is: it is edited with
`PUT /recordings/{id}/summary`, downloaded from `GET /recordings/{id}/summary` (as
"… - note.md") and deleted like any note. Retranscribe and resummarize answer 409 for it,
and it has no audio or transcript. Future note types get their own `type` value.

**Note numbers.** Every note has a `number`, counted per user like an issue number and
never reused. `RecordingRepo.Create` takes the owner's next number from the `counters`
collection (`{_id: "notes:<userId>", seq}`, incremented atomically); a unique partial index
on `(ownerId, number)` guards it. At start-up `NumberNotes` numbers the notes from before
numbers (oldest first, after ownerless notes got their owner) and first raises each counter
to the highest number in use, so it is safe to run on every start. `GET /recordings?number=12`
finds a note by number. In a note's text, "#12" links to note 12: the editor
(`SummaryEditor.tsx`) offers the notes in a menu after "#" is typed (filtered by number
prefix, or by title), inserts the chosen "#12" as plain text, so the Markdown stays plain, and
marks the references as links with decorations (Ctrl/⌘+click opens them); the read-only
Markdown renderer links them too. Links go to `/n/12`, which opens the note from the list or
looks it up on the server.

**Boards.** A board (`recording.type: "board"`, `service/boards.go`) is a kanban board of
other notes. Like a text note it is stored `summarized` with its title in `summary` (renamed
with `PUT /recordings/{id}/summary`), and is listed, labeled, moved and deleted like any
note. Its `board` field holds a **scope** (`{kind: "folder"|"label", id}`; folder `""` is the
top level, an empty kind shows nothing) and 1-20 **columns** (`{id, name, notes}`), where
`notes` are the IDs of the notes put into that column, in order. The web UI works out the
cards from the workspace list: every note in the scope (never a board) is shown in its column,
and notes in no column go to the end of the first. `POST /recordings/board` creates a board
(default columns "Todo", "In Progress", "Done"; the UI sends them translated) and
`PUT /recordings/{id}/board` replaces scope, columns and placements at once. Deleting a
folder points boards showing it at the folder its notes moved into; deleting a label clears
the scope of boards showing it (`MoveFolder` / `RemoveLabel` in the repository). A board can
also show a saved filter (`scope.kind: filter`); deleting the filter clears the scope of
boards showing it (`ClearBoardScope`).

**Saved filters** (`service/filters.go`). `GET`/`POST /filters` and `PUT`/`DELETE
/filters/{id}` keep each user's named queries (`name` unique per user ignoring case,
`query` up to 500 characters, `pinned`). The server stores queries as text and doesn't
evaluate them: the web app compiles them (`frontend/src/lib/filterQuery.ts`) against the
user's labels, folders and notes, both for the workspace list (the search box takes the same
language; plain words search the titles) and for boards that show a filter. The language
has words, `#12`, `label:`/`@`, `folder:` (and the folders in it), `due:` (`today`,
`tomorrow`, `overdue`, `week`, `month`, `none`, `any`, a date) and `due<`/`<=`/`>`/`>=`,
`done`, `task`, `p1`–`p3`, `repeat`, `estimate`, `template`, `type:`, people (`owner:`,
`from:`/`by:`/`author:`/`reporter:` for who made the note, `assignee:`, `shared:me` and
`shared:<person>` for notes others shared with the user, `shared`, `mine`; a person is `me`,
an email, its part before the @ or the start of one, resolved against `GET /me/people`, the
user and everyone they share notes or folders with), combined with `&` (or a
space), `|`, `!` and parentheses.

**Version history** (`domain/noteversion`, `service/note_versions.go`). The
`noteVersions` collection keeps notes' earlier titles and texts. `editSummary` (edits and
`RestoreVersion`) and `requeue` (re-transcribe, re-summarize) hand the text they replace to
`keepVersion`, after the change was saved: an edit keeps it only when the note's newest
version is older than 10 minutes (the web app saves every few seconds, so a stretch of typing
becomes one version), restoring and regenerating always keep it. Empty and unchanged texts
aren't kept, a note keeps its newest 50 versions (`Prune`), and deleting a note for good
deletes them. Keeping a version is best effort: a failure never fails the edit. Lists leave
out the text. Versions are in the admin backup, not in personal backups.

**Templates** (`service/note_publishing.go`, `frontend/src/lib/templates.ts`). A text note
with `template: true` (`PUT /recordings/{id}/template`) is offered when a new note is made,
next to the built-in templates, which live in the translations. The web app copies the
template's title and text into the new note and fills in the placeholders, so the server
knows nothing more about templates.

**Publishing** (`service/note_publishing.go`). `PUT /recordings/{id}/public` gives the note
a `public` link: a 256-bit random token, stored as it is (so the owner can see the link
again) and found through a unique partial index on `public.token`. `GET /public/{token}` and
`GET /public/{token}/images/{imageId}` need no sign-in; they answer 404 for unknown tokens and
for notes in the trash. The text's pictures of the note itself are pointed at the public
image route, others are dropped. The web app shows it at `/p/<token>`, outside the app's
frame. `DELETE /recordings/{id}/public` takes the link down; publishing again makes a new one.

**Writing with AI** (`service/ai_write.go`). `POST /ai/write` sends a passage of a note (the
selection as Markdown, or for `continue` the text before the cursor) with the note around it
to the summary model, with a task by `action`, and returns Markdown; nothing is saved. The
editor (`components/AiWriter.tsx`) marks the target with a decoration that follows edits
made meanwhile, shows the answer, and puts it in on request. Markdown can't keep block
content in a table cell, so an answer that isn't a single paragraph goes below the table.

**Time tracking** (`domain/timelog`, `service/timelog.go`). `PUT /recordings/{id}/estimate`
sets a task's `estimate` in minutes (and labels the note as a task). Time is logged in the
`timeEntries` collection, one entry per stretch of work on a note: `POST /timer` starts one
(stopping the running one), with `minutes` a focus session whose `until` stops it by itself;
`DELETE /timer` stops it. A user has at most one running entry (`running: true`, guarded by a
unique partial index). A focus session that ran out is closed lazily at `until` the next
time the timer or the log is read, so nothing needs to run in the background; entries also
stop after 24 hours. Stopping, adding (`POST /time-entries`) and deleting an entry adjust
the note's `trackedSeconds` with an atomic `$inc` (`AddTrackedSeconds`), so a save of the
note from an older copy doesn't lose logged time (the worker keeps `estimate` and
`trackedSeconds` like `labels`). `GET /time-entries?from=&to=` lists the entries that
started on those days in the user's time zone (the current week without them), and
`/time-entries/export` returns them as CSV. Deleting a note for good deletes its entries.

**Calendar feed** (`service/calendar.go`). `POST /me/calendar` makes a random token
(`kpc_…`) and stores its SHA-256 in `users.calendar.tokenHash` (unique, sparse index); the
link `GET /calendar/{token}.ics` is returned once, and a new one replaces it. The feed is
public but rate-limited, and lists the user's open tasks with dates (not in the trash) as
VEVENTs: all-day events for dates without a time; timed ones last their estimate (30 minutes
without one) and are written in UTC, or with `TZID` of the user's time zone when they repeat
(so the time of day survives daylight saving changes). Repeat rules become RRULEs (weeks
start on Sunday as in `Repeat.Next`; the 29th–31st of a month use `BYSETPOS=-1` to fall on
short months' last day), reminders become VALARMs, and each event links to its note.

**MCP server** (`service/mcp.go`, `transport/http/mcp_handlers.go`, `mcp_tools.go`). AI
assistants reach a user's notes through a Model Context Protocol server at `/mcp` (outside
`/api/v1`). `POST /me/mcp` makes a random token (`kpm_…`) and stores its SHA-256 in
`users.mcp.tokenHash` (unique, sparse index); the token is returned once, a new one replaces
it, and `DELETE /me/mcp` turns access off. The server speaks the Streamable HTTP transport
in its stateless form: each POST carries one JSON-RPC message (or a batch) and gets one JSON
response; notifications get `202`, and `GET`/`DELETE` (event streams, sessions) get `405`.
Without a valid token it answers `401` with `WWW-Authenticate: Bearer …, resource_metadata="…"`,
which starts OAuth in assistants that support it (below). It implements
`initialize` (agreeing on the client's protocol version when it knows it), `ping`,
`tools/list` and `tools/call`; the tools (`search_notes`, `find_notes`, `get_note`,
`list_tasks`, `list_folders`, `list_labels`, `create_note`, `update_note`, `update_task`) call
the same services as the REST API with the token's user as the account, so ownership checks
apply unchanged. Notes are named by ID or number, folders and labels by ID or name. Tool failures
(unknown note, invalid input) come back as results with `isError`, so the assistant can
correct itself; unexpected errors are logged and reported as "internal error".

**OAuth for the MCP server** (`service/oauth.go`, `transport/http/oauth_handlers.go`,
`pages/OAuthAuthorize.tsx`). Most assistants (Claude's custom connectors, ChatGPT apps) only
connect through OAuth, as the MCP authorization spec describes, so knowpod is an OAuth 2.1
authorization server for its own MCP server:

- `/.well-known/oauth-protected-resource[/mcp]` (RFC 9728) names the resource (`<base>/mcp`)
  and knowpod itself as its authorization server; `/.well-known/oauth-authorization-server`
  (RFC 8414) lists the endpoints. The base URL is the request's (`X-Forwarded-Proto` honored).
- `POST /oauth/register` is dynamic client registration (RFC 7591, 20 per hour and IP).
  Redirect URIs must be `https`, `http` on loopback (any port matches, RFC 8252) or a native
  app's custom scheme. Clients authenticating at the token endpoint (`client_secret_basic`,
  the default, or `client_secret_post`) get a secret (`kps_…`, only its SHA-256 is stored);
  public clients (`none`) rely on PKCE.
- `/oauth/authorize` is a page of the web app (behind the sign-in). It checks the request
  with `GET /api/v1/oauth/authorize`: an unknown client or redirect URI is shown as an error,
  other problems (no PKCE `S256`, `response_type` not `code`, a `resource` other than this
  MCP server) go back to the client. The user allows or declines; `POST
  /api/v1/oauth/authorize` answers the address the browser returns to, with a code (valid 5
  minutes) or `error=access_denied`, plus the client's `state`.
- `POST /oauth/token` (form-encoded) exchanges a code (once, atomically, checking client,
  redirect URI and PKCE verifier) or a refresh token for an access token (`kpo_…`, 1 hour) and
  a refresh token (`kpr_…`, 90 days). Refresh tokens rotate: each is used once.
  `POST /oauth/revoke` (RFC 7009) ends a grant.
- These endpoints and `/mcp` allow any CORS origin (they don't use cookies), for assistants
  running in a browser.

The user sees the connected assistants under **Settings → Account → AI assistants**
(`GET /me/mcp/apps`) and can disconnect each (`DELETE /me/mcp/apps/{id}`); deleting a user
deletes their grants. The personal access token stays for tools without OAuth.

**Labels** (`service/labels.go`). `GET /labels` lists the built-in labels (only `task`,
named by the UI in its language) and the user's own; `POST`/`PUT`/`DELETE /labels/{id}`
manage their own (built-in ones can't be changed). `PUT /recordings/{id}/labels` replaces a
note's label IDs (`RecordingService.SetLabels` checks each is built-in or the note owner's);
`PUT /recordings/{id}/done` sets the check mark, which only notes labeled `task` have, and
taking `task` off clears it. Deleting a label pulls it off the owner's notes
(`RecordingRepository.RemoveLabel`). Since labels change at any time, also while a note is
being processed, the worker copies `labels` and `done` from the stored document before
saving a stage's result (`Recording.KeepUserFields`), so a long transcription can't undo them.

**Folders** (`service/folders.go`). `GET /folders` lists the user's folders; each has an
optional `parentId`, so folders nest (at most 8 deep). `POST`/`PUT /folders/{id}` create,
rename and move them; names are unique among the folders in the same place, and a folder
can't be moved into itself or one of its own folders. `DELETE /folders/{id}` moves the
folder's notes (`RecordingRepository.MoveFolder`) and folders up into its parent.
`PUT /recordings/{id}/folder` moves a note (`folderId`, empty for the top level);
`folderId` is kept by the worker like `labels` and `done`.

**Sub-notes** (`service/recordings.go`). A note's optional `parentId` makes it a sub-note of
another of the owner's notes, like a folder whose head is a note itself. A note is either in
a folder (`folderId`) or under a note (`parentId`), never both: `PUT /recordings/{id}/parent`
sets the parent and clears the folder, and `PUT /recordings/{id}/folder` does the reverse.
A note can't go under itself or one of its own sub-notes, and notes nest at most 8 deep.
`POST /recordings/text` takes an optional `parentId` to create a sub-note directly. Deleting
a note moves its sub-notes to where it was (`RecordingRepository.MoveSubNotes`); `parentId`
is kept by the worker like `folderId`.

**Tasks** (`domain/recording/task.go`, `service/tasks.go`). A note labeled `task` has, besides
`done`, an optional `due` (`date` YYYY-MM-DD, `time` HH:MM, `repeat`, `remind` in minutes
before) and a `priority` (1–3). Dates are calendar days in the owner's time zone
(`user.timeZone`, set by the web app from the browser through `PUT /me/preferences`), so a
repeat like "every Monday at 9:00" stays at 9:00 across daylight saving changes.
`PUT /recordings/{id}/due` and `/priority` label the note as a task; taking the label off
clears `done`, `due` and `priority`. `POST /recordings/text` takes the same fields for quick
add. `Repeat.Next` steps a date by day, weekday (Mon–Fri), week (optionally on listed
weekdays), month (keeping `monthDay`, clamped to short months, or with `nth` on the nth of one
weekday in `weekdays`, -1 for the last: "every third Friday") or year; checking off a
recurring task (`SetDone`) keeps it open and moves it to its first occurrence from today on
(`Due.Advance`). The natural-language dates are parsed in the web app
(`frontend/src/lib/dateParse.ts`); the API only takes the structured form.

**Reminders** (`service/notifications.go`). Every change of a task's date, reminder or check
mark recomputes `remindAt`, the UTC moment its next reminder is due (the due time, or 9:00
for a day without a time, minus `remind`), or clears it when there is none, it is done, or
the moment has passed. A time zone change recomputes the user's pending reminders
(`RescheduleReminders`, via `AuthService.OnTimeZoneChanged`). `NotificationService.Run`
(every 30 s, started in `main.go`) takes due reminders with `ClaimReminder`, an atomic
`findOneAndUpdate` that unsets `remindAt`, so each reminder is sent at most once even across
restarts, and sends `{title, body, url, tag}` in the owner's language to all their push
subscriptions and live connections. `remindAt`, `due` and `priority` are kept by the worker like `labels`.

**Web Push.** The VAPID key pair is generated on the first start and stored in `settings`
(`InitWebPush` only inserts, so it never changes; browsers subscribed with its public key).
`GET /me/notifications` returns the public key and the user's devices;
`POST /me/notifications/subscriptions` stores a browser's `PushSubscription` in
`pushSubscriptions` (ID = first 16 bytes of the SHA-256 of the endpoint, at most 20 per
user). Endpoints must be on a browser push service's host (`pushServiceHosts`), so the
server never sends requests to hosts a user picked. `webpush.Sender` encrypts each message for the subscription (aes128gcm, one record)
and signs a VAPID JWT (ES256) for the push service's origin; a 404/410 answer deletes the
subscription. In the browser, `public/push-sw.js` is imported into the generated service
worker: it shows the notification and, on click, focuses the app and asks it to open the
note (or opens a new window).

**Live notifications (desktop app).** Electron has the Push API but no push service, so the
desktop app can't subscribe. Instead the web app, when it runs in a desktop app that offers
`knowpodDesktop.notify` (`frontend/src/lib/desktop.ts`, used by `Layout`), keeps an
`EventSource` on `GET /me/notifications/stream`. `NotificationService.Listen` registers a
buffered channel per connection (at most 10 per user, in memory, so this needs the single
instance the server runs as anyway); `Notify` hands each message to the user's channels
without blocking, then to Web Push, and counts both in `sent` (`listening` in
`GET /me/notifications` counts the connections). The handler writes `notification` events
and a comment every 25 s; `NotificationService.Shutdown`, registered with
`http.Server.RegisterOnShutdown`, ends the streams so shutdown doesn't wait for them. The
desktop app's main process (`desktop/src/main.js`) shows each message as a native
notification, accepting them only from pages of the configured server; a click shows the
window and sends `knowpod:open` to the page, which navigates to the note. Closing the window
hides it (the page, and so the stream, keeps running) and a tray icon offers Open, Quit,
**Keep Running When Closed** and **Start at Login** (started with `--hidden`, it stays in
the tray).

**Speakers.** Transcripts start each turn with a speaker label ("Speaker 1:", after the time
stamp); the transcription prompt asks the model to tell voices apart and to label every line.
Each later 5-minute piece is sent with the labels used so far and the end of the previous
piece's transcript (`continuationPrompt`), so the same people keep their labels. The summary prompt also asks for `speakers`: the names the conversation makes clear
for those labels, kept in `summary.speakers` when the label is in the transcript
(`parseSpeakers`). `POST /recordings/{id}/speakers/rename` (`service/speakers.go`) replaces a
label where it starts lines of the transcript and where the summary and the action items
mention it as a whole word (a mention already followed by the rest of the new name stays),
drops the suggestion for it and counts up the note's `revision`, since the text changed.
Naming a speaker like another merges them. `PUT /recordings/{id}/speakers` (`NameSpeakers`,
used by the note's sidebar) names several at once; with `resummarize` it renames only the
transcript and requeues the note at `transcribed` (via `requeueAs`, for editors too), so the
summary is made again from the named transcript, and the summary it replaces is kept as a
version. The new summary is made with the language, model and theme of the one it replaces:
they go into the note's `nextSummary`, which overrides `summaryOptions` for the next summary
only and is cleared once it is made (and by resummarizing or retranscribing). Names can't change while the note is in the pipeline, since the stage would save
its result over them.

**Ask** (`service/ask.go`). `POST /ask` answers a question from the notes the account sees,
without an index, in two calls to the summary model:

1. **Search.** The model gets a catalog of up to 800 notes, newest first, one line each
   (`c12 | date | kind | title | the start of the text`, shortened to fit 120,000
   characters) and the question (with earlier questions of a follow-up), and answers with
   up to 12 catalog references that fit by meaning and up to 16 keywords (names, synonyms,
   English and German). The keywords and the question's longer words are counted in each
   note's full text (title, summary, action items, transcript). The picked notes come first,
   then the ones with the most keyword hits (a third of the places are kept for them), 8 in
   all.
2. **Answer.** The model gets those notes, each with its reference number: the summary or
   text, then the transcript, cut to its share of 80,000 characters as excerpts around the
   keywords (whole lines, the start always kept). It answers in Markdown citing `[1]`, `[2]`,
   and names a supporting quote and, for transcripts, its time stamp per source.
   `parseAnswer` keeps the sources the answer cites; a time stamp becomes `offsetMs` in a
   recording with audio. The web app links citations to the notes (`/conversations/{id}?t=`
   plays from the moment).

`AskService.Find` runs the search step alone for the MCP tool `find_notes`, returning the
notes with a passage around the keywords.

**Briefings** (`service/briefing.go`). `users.briefing` holds a user's settings (`dailyOff`
— the daily briefing is on unless turned off —, `weekly`, `time`, `weeklyDay`, `noNotify`,
`sections`, `actionItemDays`) and the days the last briefing and review were made
(`lastDaily`, `lastWeekly`, in the user's time zone). `BriefingService.Run` (every minute,
started in `main.go`) makes the ones due: past today's time, not made today, and for the
review on its day. Each is marked made before it is made (on a fresh copy of the user, so
settings changed meanwhile stay), so it is made once even when making it fails; turning
briefings on after today's time marks today, so the first comes the next day.

The daily briefing is no note: it is kept in `briefing.today` (`day`, `title`, `markdown`,
`summary`, `madeAt`) and shown on the web UI's home page (`/briefing`, also shown at `/`
on desktop). `GET /me/briefing/today` returns it, making it right away when there is none
for today yet (without announcing it; the scheduled one made later that day replaces it
and is announced); `POST /me/briefing/today` makes it again. Unless `noNotify` is set, the
scheduled one is announced through `NotificationService.Notify`, linking to `/briefing`.
Besides the tasks due today, its `sections` (default: `overdue`, `new`, `digest`,
`actionItems`; also `upcoming`, the tasks due in the next week) say what it lists; action
items are looked for `actionItemDays` back (default 7, at most 30).

The weekly review is a text note with `source: briefing` in the user's folder **Briefings**
(found by name or made, remembered in `briefing.folderId`), announced through
`NotificationService.Notify`; `POST /me/briefing/run` with `kind: weekly` makes one right away.

The briefings' sections are put together from the user's notes: open tasks by date and
priority, notes created since the last daily briefing (at most a week), action items
neither made tasks nor dismissed, tasks checked off (`doneAt`, set by `SetDone`), time
entries, and tasks without a date unchanged for 30 days. The summary model writes the
digest of the new notes' summaries, citing `#12`; references to notes it wasn't given lose
their `#`. Without a configured model the briefing has no digest.

**Action items.** The summary's `actionItems` (`id`, `text`, `owner`, `due`) are offered below
the summary. `POST /recordings/{id}/action-items/{itemId}/task` creates a text note under the
note, labeled `task`, due on the item's date with a reminder at 9:00, and stores its ID as
the item's `taskId`; `PUT …/dismissed` hides an item. Re-summarizing replaces the items.

`GET /recordings/{id}/summary` and `/transcript` return the texts as `.md` / `.txt`
downloads (`transport/http/downloads_handlers.go`), or JSON with `?format=json`.

### Themes and summary options (`service/themes.go`)

The summary prompt is assembled per recording (`summarySystemPrompt` in `service/ai.go`):
the fixed JSON reply format, the **theme's instructions** (the structure of the Markdown
summary) and the **output language**. `recording.summaryOptions` (`language`, `model`,
`themeId`) choose them; empty values mean auto-detect, the configured summary model and the
Auto theme. `POST /recordings/{id}/resummarize` with a body validates and stores new
options before requeueing. `ThemeService.Resolve` finds built-in themes by ID and a user's
own theme only for its owner; anything else (e.g. a deleted theme) falls back to Auto. The
summary stores `themeId`, `themeName`, `language` and `model`, so the UI can show what a
summary was made with.

**Editing.** `PUT /recordings/{id}/summary` (`RecordingService.EditSummary`) replaces the
title and Markdown text and sets `summary.editedAt`; theme, language and model stay for
reference. Markdown remains the only stored format.

In the web app the summary is always an editable document (there is no view/edit mode):
`components/SummaryEditor.tsx` is a TipTap (ProseMirror) editor with the official
`@tiptap/markdown` extension, and the page title is an in-place input. There is no
toolbar: a slash menu (`@tiptap/suggestion`, triggered by `/` at the start of a line,
rendered by React through a small bridge object) inserts blocks, and a BubbleMenu
(`@tiptap/react/menus`) on text selections formats inline. Empty paragraphs serialize as
`&nbsp;` lines, which the editor strips before saving. Saving lives in
`hooks/useAutosave.ts`, shared by both:

- The unchanged baseline is the editor's own Markdown right after loading (not the stored
  text), so normalization (e.g. `*` → `-` bullets) never counts as an edit or triggers a
  save.
- A change is sent 2 s after the last keystroke and at least every 10 s; saves are
  serialized (a change made during a save goes out with the next one); failed saves are
  retried every 10 s and on the browser's `online` event; leaving the note saves what's
  left, and closing the tab with unsaved changes asks first.
- Regenerating or re-transcribing calls `discard()` after the confirmation, so a pending
  save can't write the old text over the new summary.
- The note's body is keyed by the summary's `createdAt` (`pages/Conversation.tsx`): own
  saves keep the editor as it is, a regenerated summary remounts it with the new text. The
  summary tab stays mounted while other tabs are shown, so unsaved text and undo history
  survive tab switches.

The editor bundle (~170 KB gzipped) is lazy-loaded; until it arrives, the summary is shown
with the small, HTML-free read-only renderer in `components/Markdown.tsx`, which covers
everything the editor produces: headings, nested bullet, numbered and task lists (`- [ ]`,
`- [x]`), quotes, rules, code blocks, links (http, https and mailto only), bold, italic,
strikethrough and code.

Built-in themes are defined in code with English instructions; the UI translates their
names and descriptions by ID. Users' themes live in the `themes` collection. A user's
version of a built-in theme is a `themes` document with `builtInId` set: `PUT
/themes/<built-in id>` creates or updates it, `DELETE /themes/<built-in id>` removes it
(reset). `List` and `Resolve` substitute it for the built-in theme for that user only
(`customized: true`), and it can't be used by its own document ID.

## Adding a processing stage

For example, extracting tasks after the summary:

1. Add a status in `domain/recording/recording.go`, e.g. `StatusTasksExtracted`, and a
   field for the result.
2. Implement the stage in `service`, e.g. `Extractor.Run(ctx, rec)`. It can use
   `rec.Transcript`, or read the audio through `ports.ObjectStore` using `rec.Audio.Key`.
   Keep it idempotent.
3. Register it in `cmd/server/main.go`, e.g. in the AI worker after the summary stage:

   ```go
   {Name: "extract-tasks", From: recording.StatusSummarized, To: recording.StatusTasksExtracted,
    Run: extractor.Run, Enabled: extractor.Configured},
   ```

4. Test it the way `worker/worker_test.go` does, with the in-memory repositories and
   object store.

Retries, backoff, leases and failure handling come from the worker; the stage only
implements the work.

## Data model

**`devices`**

| Field | Notes |
|---|---|
| `_id` | Random 24-hex ID |
| `ownerId` | The user whose recordings it uploads (indexed) |
| `name` | Label given at registration |
| `tokenHash` | SHA-256 of the token (unique index). Tokens are 256-bit random values, so an unsalted fast hash is enough. |
| `createdAt`, `lastSeenAt`, `revokedAt` | `lastSeenAt` is updated at most once a minute |

**`recordings`**

| Field | Notes |
|---|---|
| `_id` | Random 24-hex ID; the device's `uploadId` |
| `ownerId` | The user it belongs to (indexed with `createdAt` for lists) |
| `deviceId`, `clientId` | Device (or `pocket:<userId>` / `upload:<userId>` / `text:<userId>` / `board:<userId>` / `remarkable:<userId>`) and its `recordingId` (unique together) |
| `type` | The kind of note: absent for audio recordings, `text` for text notes (see below), `document` for reMarkable documents, `board` for boards |
| `board` | A board's scope and columns (see **Boards** above) |
| `file`, `pages` | A document's PDF or EPUB (S3 key, content type, size) and its page count |
| `sourceRevision` | Content hash of the imported version of a reMarkable document |
| `tablet` | A text note's copy on the owner's reMarkable: `documentId`, the `revision`, `name` and cloud `parent` sent last, `removed` (in the tablet's trash), `sentAt`, `error` |
| `labels`, `done` | IDs of the note's labels (see below) and the check mark of a `task` note |
| `estimate`, `trackedSeconds` | A task's estimate in minutes, and the time logged on the note (finished entries) |
| `folderId` | The folder the note is in; absent at the top level |
| `shares`, `members` | Direct shares (`userId`, `role`, `createdAt`) and all members (`userId`, `role`, `root`, and the member's own `folderId`, `labels`, `position`, `remindAt`); see **Sharing** |
| `createdBy` | The editor who made the note in someone else's shared note |
| `version`, `revision` | Counted up by every change, and by every change of the title and text; see **Concurrent changes** |
| `status` | `uploading`, `received`, `stored` or `failed` |
| `size`, `sha256` | Declared by the device at create |
| `recordedAt` | Optional, from the device |
| `format` | Sample rate, channels, bits, frames, duration. Set once received. |
| `audio`, `original` | S3 key, content type and size of the FLAC and the optional WAV |
| `transcript` | `text` (with `[m:ss]` time stamps per speaker turn), `model`, `createdAt` |
| `highlights` | Moments marked on the device: `offsetMs` from the start, optional `at` (wall-clock) |
| `summary` | `title` (the conversation's name in the UI), `markdown`, `model`, `language`, `themeId`, `themeName`, `createdAt`, `editedAt` (set by a person's edit). Lists leave out `transcript` and `summary.markdown`. |
| `summaryOptions` | `language`, `model`, `themeId` chosen under Summary details (empty = defaults) |
| `attempts`, `notBefore`, `lastError` | Worker bookkeeping |
| `createdAt`, `updatedAt`, `receivedAt`, `storedAt` | Timestamps (UTC) |

Indexes: `(deviceId, clientId)` unique; `(status, notBefore)` for claiming;
`(status, updatedAt)` for stale uploads; `createdAt` for listing; `members.userId` for the
notes shared with a user and `members.remindAt` for their reminders.

**`users`**

| Field | Notes |
|---|---|
| `_id` | Random 24-hex ID |
| `email` | Lower-case (unique index) |
| `role` | `admin` or `user` |
| `language` | Web UI language (`en`, `de`); empty follows the browser |
| `passwordHash` | bcrypt; empty for the built-in admin until a password is set (then `ADMIN_PASSWORD` applies) |
| `pocket.webhookId` | Random part of the user's webhook URL (unique, sparse index) |
| `pocket.webhookSecret`, `pocket.apiKey` | The user's Pocket credentials; never returned by the API |
| `calendar.tokenHash`, `calendar.createdAt` | SHA-256 of the calendar feed token (unique, sparse index) and when it was made |
| `mcp.tokenHash`, `mcp.createdAt` | SHA-256 of the MCP access token (unique, sparse index) and when it was made |
| `createdAt`, `passwordChangedAt` | Timestamps (UTC) |

**`sessions`**

| Field | Notes |
|---|---|
| `_id` | SHA-256 of the session token |
| `userId` | The signed-in user (indexed, to end all their sessions) |
| `createdAt`, `expiresAt` | TTL index on `expiresAt` deletes expired sessions |

**`oauthClients`**: the assistants registered through dynamic client registration: `_id`
(the client ID), `name`, `redirectUris`, `authMethod`, `secretHash` (SHA-256; public clients
have none), `createdAt`.

**`oauthGrants`**: the access users gave assistants: `_id`, `userId` (indexed with
`createdAt`), `clientId`, `scope`, `redirectUri`, `codeChallenge`, and the SHA-256 of the
authorization code (`codeHash`, until exchanged), the access token (`accessHash`, expiring at
`accessExpiresAt`) and the refresh token (`refreshHash`), each in a unique, sparse index;
`createdAt`, `lastUsedAt`, and `expiresAt` (the code's, then the refresh token's expiry; TTL
index).

**`themes`**: users' own summary themes: `_id`, `ownerId` (indexed with `name`), `name`,
`description`, `instructions`, `builtInId` (set for a user's version of a built-in theme),
`createdAt`, `updatedAt`.

**`labels`**: users' own note labels: `_id`, `ownerId` (indexed with `name`), `name`
(unique per user, ignoring case), `color` (`#rrggbb`), `createdAt`, `updatedAt`.

**`folders`**: users' folders: `_id`, `ownerId` (indexed with `name`), `name`,
`parentId` (absent at the top level), `position`, `shares` (`userId`, `role`, `createdAt`;
`shares.userId` indexed; see **Shared folders**), `createdAt`, `updatedAt`.

**`filters`**: users' saved filters: `_id`, `ownerId` (indexed with `name`), `name`,
`query`, `pinned`, `createdAt`, `updatedAt`.

**`timeEntries`**: time logged on notes: `_id`, `ownerId` (indexed with `start`), `noteId`
(indexed), `start`, `end` (absent while running), `until` (a focus session's end), `running`
(set only while running; unique per owner), `createdAt`.

**`tablets`**: users' reMarkable links, `_id` = user ID: `deviceToken` (never returned by
the API), `pairedAt`, `rootHash` and `items` (the cache of the last pull), `folderId` (the knowpod
folder new notes go into), `lastPullAt`,
`lastError`, `lastResult`.

**`settings`**: one document per settings group. `_id: "openrouter"` holds `apiKey`,
`transcriptionModel`, `summaryModel`, `documentModel` and `updatedAt`.

`repository/mongo/setup.go` creates collections and indexes on every start.

The `TxManager` in `repository/mongo/client.go` provides multi-document transactions (the
reason MongoDB runs as a replica set). Nothing uses it yet.

## Tests

| Test | Covers |
|---|---|
| `audio/*_test.go` | WAV parsing edge cases; FLAC output decodes to the exact input samples |
| `service/themes_test.go`, `auth_test.go` | Themes (built-ins, own themes, isolation, fallback to Auto), summary prompts with theme/language/model, validation of summary options, language preference |
| `service/*_test.go` | Users (built-in admin, create/update/delete with cascade, last-admin and self protection), per-user Pocket settings, browser uploads (formats, limits), ownership checks; upload protocol: chunks, idempotency, offsets, dropped connections, checksum reset, invalid audio, isolation between devices, purge; device tokens; sign-in with the default login, password change overriding it, session expiry and logout; transcription (FLAC chunks, passthrough, size limit), summaries and their parsing, OpenRouter settings and model filtering, delete/re-transcribe/re-summarize |
| `remarkable/*_test.go` | Page files (v6 blocks, deleted items, erasers, highlighter colors; v5), typed text (insertions, concurrent inserts, deletions, formatting items, paragraph styles), index schemas, page order, PDF structure (cross-references, page size, title), PNG rendering, and the client against a fake cloud (`remarkabletest`): pairing, revoked tokens, file names, download limits, writing documents (new, changed with the tablet's files kept, moved to the trash, made again when gone, redone after another device's change) and note EPUBs (well-formed, stable bytes, escaping, checklists) |
| `service/remarkable_test.go` | Pairing, all documents except trashed and deleted ones, the knowpod folder (created, reused, renamed, made again), mirrored cloud folders (created, renamed, moved, same names, cycles, depth, earlier imports) and notes moving with their documents, unchanged accounts costing two requests, rename vs. content change, notes in processing left alone, fetch and store of notebooks and PDFs, reading pages with a vision model, typed text (alone and next to handwriting), ignored document names, edited summaries kept, sending text notes (folders, one root update, unchanged notes cost no requests, pulls leave copies out, edits, moves out and back, trash, documents deleted on the tablet, failures, nudges waiting for quiet), the reMarkable folder kept while paired |
| `worker/worker_test.go` | Archive stage end to end, retry/backoff, permanent failure, recovery, disabled stages waiting |
| `openrouter/*_test.go` | Request shape for audio, error handling, model list |
| `audio/speech_test.go` | Speech chunks: count, duration, mono 16 kHz output |
| `transport/http/*_test.go` | Full HTTP flows on in-memory storage: device uploads, browser sign-in with cookies, password change and reset, user management, access control per role, isolation between users, browser uploads, per-user Pocket webhooks, reMarkable pairing, import and document file (ranges, framing), audio ranges, login rate limit. `openapi_test.go` fails if a route under `/api/v1` is missing from `openapi.yaml` or the spec lists a route that doesn't exist. |
| `repository/mongo/repo_test.go` | Real MongoDB; runs only with `KNOWPOD_TEST_MONGO_URI` set |

The S3 store has no automated test. It has been checked by hand against an S3-compatible
server.

## Notes view

`pages/NotesLayout.tsx` is a layout route for `/` and `/conversations/:id`: the workspace list
(`components/NotesList.tsx`) as a sidebar and the open note (`pages/Conversation.tsx`, or a
placeholder) in the main area. `context/NotesContext.tsx` holds the list for both: it polls
while any note is processing, and the open note pushes its changes into it (`upsert`, e.g.
a renamed title after an autosave) and removes itself when deleted. Below 900 px the layout
shows one pane at a time with CSS only: the list at `/`, the note (with a back link) when
one is open. Responses for a note that is no longer open are ignored, so switching notes
quickly can't show the wrong one.

Each list entry shows an icon for the note's type (`NoteIcon` in `components/Icons.tsx`: a
sound wave for audio, lines of text for text notes). The list's **+** button creates an
empty text note and opens it with its title selected; a text note's page has only the
editor, with download, copy and delete (no view switcher, transcript, source or AI actions).
The board button next to it creates a board (`components/Board.tsx`) and opens it: a scope
picker, then the columns side by side. Cards are dragged between and within columns (or
moved with their arrow buttons on touch screens), columns are renamed, added and deleted,
and every change is saved at once with `PUT /recordings/{id}/board`.

Labels (`components/Labels.tsx`) show as colored chips under the note's title; a popover
toggles them and creates new ones, and **Settings → Labels** renames, recolors and deletes
them. `NotesContext` loads the labels once for the list and the open note. A note labeled
Task gets a check box over its icon in the list (outside the link, so checking doesn't open
the note; the change shows at once and is undone if saving fails) and a Done/To do box in
its header (or its sidebar, see below); the open note takes over check marks and labels changed in the list. Saves that
answer after their note was left (e.g. the autosave on leaving) only update the list, never
the note now open.

A note's page (`pages/Conversation.tsx`) puts its icon actions on the row with its date and
labels, and its save state in the top right corner. Notes other than boards get a sidebar
(`.note-aside`) with the icon actions, task, labels and details; a CSS container query on
the note's own width (`.conversation.with-aside`) shows it only when there is room, and then
hides the icon actions, labels, number, date and the other repeated details from the header.
Both places render the same components, so either one edits the note.

The list switches (remembered per browser) between **Created**, notes grouped by the day
they were made; **Due** (`components/DueView.tsx`), the notes with a due date, open or done,
grouped by that date; **Folders** (`components/FolderTree.tsx`), a tree of folders with the
notes in the order the user put them (`position`, set with `PUT /recordings/order` and
`PUT /folders/order`; unordered ones follow by title); and **Tasks**, the open tasks.
New notes made in the folder view go into the folder opened last (`lib/lastFolder.ts`).
Folders are created, renamed and deleted in place; notes and folders are moved by drag and
drop (onto a folder, or the free space for the top level; onto a row's top or bottom edge
to put it before or after it, or Alt+Up/Down), and a note's toolbar has a
**Move to folder** menu (`components/MoveToFolder.tsx`) that also works on touch screens.
While searching, only folders with matching notes are shown, opened. The note's save state
is a colored dot at the far right of its toolbar (green saved, amber unsaved, pulsing while
saving, red on failure: click to retry); the words are its tooltip and a screen reader
status.

## Errors

API errors are `{"error": "...", "code": "..."}`. `code` is a stable identifier
(`writeErr` in `transport/http/response.go` maps service errors to status and code, most
specific first); the web UI translates it (`lib/errors.ts`, keys `errors.<code>`) and falls
back to the English `error` text for unknown codes.

## Web app

The React app (`frontend/`) is built with Vite and embedded into the binary. It is
responsive down to phone width (the navigation collapses into a menu button below 760 px;
form fields use 16 px text so iOS doesn't zoom) and installable as a PWA: `vite-plugin-pwa`
generates the manifest and a Workbox service worker that precaches the app shell and falls
back to `index.html` for client-side routes, but never for `/api/*` or `/healthz`, so data
is always live. `src/lib/updates.ts` registers it and looks for a new version every half hour
and when the app comes back to the front; a new version takes over at once in the first
seconds after start, else when the app goes to the background (the desktop app's window
lives on in the tray, so it would otherwise keep running the old version). All texts are translated with `react-i18next` (`src/i18n/en.ts` and
`de.ts`; the German file is typed against the English one, so a missing key is a compile
error). The language is the user's saved `language` (from `GET /auth/me`), else the one last
used on the device, else the browser's; dates and numbers follow it. `internal/web` serves `sw.js` and the manifest with
`Cache-Control: no-cache` (so updates reach installed apps) and hashed `/assets/*` as
immutable.
