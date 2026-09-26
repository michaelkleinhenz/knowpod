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
| `repository/mongo`, `storage/s3` | Production implementations of the ports. |
| `repository/memory`, `storage/memory` | In-memory implementations, used by the tests. |
| `transport/http` | Router, authentication middleware, handlers. Maps errors to HTTP status codes. |
| `cmd/server` | Reads config, connects everything, starts the HTTP server and background jobs. |

## Users, ownership and authentication

Every recording and device has an `ownerId`: the user it belongs to. Users see and act only
on their own data; the services check this with `Account.Owns` / `Account.OwnerFilter`
(`service/auth.go`), and anything owned by someone else answers `404`, as if it didn't exist.

There are four kinds of callers:

| Caller | Credential | Checked by | Grants |
|---|---|---|---|
| Recorder gadget | Device token (`Authorization: Bearer kpd_…`) | `requireDevice` → `DeviceService.Authenticate` | `/uploads/*`; recordings belong to the device's owner |
| User in the web UI | Session cookie `knowpod_session` | `requireUser` → `AuthService.Authenticate` | Own devices, recordings, Pocket settings, password |
| Admin in the web UI | Session cookie, user with role `admin` | `requireAdmin` | The above for their own data, plus `/admin/*` (users, OpenRouter) |
| Script | `ADMIN_TOKEN` (`Authorization: Bearer …`) | `requireUser` / `requireAdmin` | Everything above, acting as the built-in admin, with `All` set: lists and lookups cover every user's data |
| Pocket | HMAC signature, secret of the user named by the webhook URL | `handlePocketWebhook` | Queue recordings for that user |

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
  Pocket webhook          pocket-fetch stage OK
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
| `remote` | Announced by a Pocket webhook; the audio is still at Pocket. |
| `uploading` | Partial WAV in the spool (`UPLOAD_DIR/<id>.wav`). Its file size is the upload offset. |
| `received` | Complete audio in the spool, waiting for the worker: a verified WAV upload, or a file fetched from Pocket (`<id>.download`, any format). |
| `stored` | FLAC (and optionally the WAV) in S3. The spool files are gone. Waits for transcription. |
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

### The archive stage (`service/archive.go`)

`received → stored`:

### Browser uploads (`service/manual_upload.go`)

`POST /api/v1/recordings` streams the raw request body into the spool (`<id>.download`),
hashing it and keeping the first bytes to identify the format. Only WAV (which must parse
as integer PCM) and MP3 are accepted. The recording is created directly in `received` with
`source` `upload`, the file name as title and `X-Recorded-At` as recording time, and the
worker takes it from there like a fetched Pocket file.

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
`/api/v1/openapi.json`. The web UI's Status page reads the JSON and lists every operation
grouped by its first tag. When you add or change a route, update the spec; the route
coverage test enforces it.

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
  prompt; the texts are joined.
- **summarize** (`transcribed → summarized`): sends the transcript with a prompt that asks
  for a JSON object with `title` and a Markdown `summary` in the transcript's language
  (`response_format: json_object`). `parseSummary` tolerates code fences and surrounding
  text. Empty transcripts get "No speech detected" without a model call.

The OpenRouter settings (key and models) live in the `settings` collection and are read on
every stage run, so changes apply immediately.

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

Built-in themes are defined in code with English instructions; the UI translates their
names and descriptions by ID. Users' themes live in the `themes` collection.

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
| `deviceId`, `clientId` | Device (or `pocket:<userId>` / `upload:<userId>`) and its `recordingId` (unique together) |
| `status` | `uploading`, `received`, `stored` or `failed` |
| `size`, `sha256` | Declared by the device at create |
| `recordedAt` | Optional, from the device |
| `format` | Sample rate, channels, bits, frames, duration. Set once received. |
| `audio`, `original` | S3 key, content type and size of the FLAC and the optional WAV |
| `transcript` | `text`, `model`, `createdAt` |
| `summary` | `title` (the conversation's name in the UI), `markdown`, `model`, `language`, `themeId`, `themeName`, `createdAt`. Lists leave out `transcript` and `summary.markdown`. |
| `summaryOptions` | `language`, `model`, `themeId` chosen under Summary details (empty = defaults) |
| `attempts`, `notBefore`, `lastError` | Worker bookkeeping |
| `createdAt`, `updatedAt`, `receivedAt`, `storedAt` | Timestamps (UTC) |

Indexes: `(deviceId, clientId)` unique; `(status, notBefore)` for claiming;
`(status, updatedAt)` for stale uploads; `createdAt` for listing.

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
| `createdAt`, `passwordChangedAt` | Timestamps (UTC) |

**`sessions`**

| Field | Notes |
|---|---|
| `_id` | SHA-256 of the session token |
| `userId` | The signed-in user (indexed, to end all their sessions) |
| `createdAt`, `expiresAt` | TTL index on `expiresAt` deletes expired sessions |

**`themes`**: users' own summary themes: `_id`, `ownerId` (indexed with `name`), `name`,
`description`, `instructions`, `createdAt`, `updatedAt`.

**`settings`**: one document per settings group. `_id: "openrouter"` holds `apiKey`,
`transcriptionModel`, `summaryModel` and `updatedAt`.

`repository/mongo/setup.go` creates collections and indexes on every start.

The `TxManager` in `repository/mongo/client.go` provides multi-document transactions (the
reason MongoDB runs as a replica set). Nothing uses it yet.

## Tests

| Test | Covers |
|---|---|
| `audio/*_test.go` | WAV parsing edge cases; FLAC output decodes to the exact input samples |
| `service/themes_test.go`, `auth_test.go` | Themes (built-ins, own themes, isolation, fallback to Auto), summary prompts with theme/language/model, validation of summary options, language preference |
| `service/*_test.go` | Users (built-in admin, create/update/delete with cascade, last-admin and self protection), per-user Pocket settings, browser uploads (formats, limits), ownership checks; upload protocol: chunks, idempotency, offsets, dropped connections, checksum reset, invalid audio, isolation between devices, purge; device tokens; sign-in with the default login, password change overriding it, session expiry and logout; transcription (FLAC chunks, passthrough, size limit), summaries and their parsing, OpenRouter settings and model filtering, delete/re-transcribe/re-summarize |
| `worker/worker_test.go` | Archive stage end to end, retry/backoff, permanent failure, recovery, disabled stages waiting |
| `openrouter/*_test.go` | Request shape for audio, error handling, model list |
| `audio/speech_test.go` | Speech chunks: count, duration, mono 16 kHz output |
| `transport/http/*_test.go` | Full HTTP flows on in-memory storage: device uploads, browser sign-in with cookies, password change and reset, user management, access control per role, isolation between users, browser uploads, per-user Pocket webhooks, audio ranges, login rate limit. `openapi_test.go` fails if a route under `/api/v1` is missing from `openapi.yaml` or the spec lists a route that doesn't exist. |
| `repository/mongo/repo_test.go` | Real MongoDB; runs only with `KNOWPOD_TEST_MONGO_URI` set |

The S3 store has no automated test. It has been checked by hand against an S3-compatible
server.

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
is always live. All texts are translated with `react-i18next` (`src/i18n/en.ts` and
`de.ts`; the German file is typed against the English one, so a missing key is a compile
error). The language is the user's saved `language` (from `GET /auth/me`), else the one last
used on the device, else the browser's; dates and numbers follow it. `internal/web` serves `sw.js`, `registerSW.js` and the manifest with
`Cache-Control: no-cache` (so updates reach installed apps) and hashed `/assets/*` as
immutable.
