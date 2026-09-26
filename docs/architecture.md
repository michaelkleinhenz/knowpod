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

## Authentication

There are three kinds of callers, each with its own credential:

| Caller | Credential | Checked by | Grants |
|---|---|---|---|
| Recorder gadget | Device token (`Authorization: Bearer kpd_…`) | `requireDevice` → `DeviceService.Authenticate` | `/api/v1/uploads/*`, own uploads only |
| Person in the web UI | Session cookie `knowpod_session` | `requireUser` → `AuthService.Authenticate` | `/api/v1/auth/me`, `/auth/password`, and the admin API |
| Script | `ADMIN_TOKEN` (`Authorization: Bearer …`) | `requireAdmin` | The admin API |

`requireAdmin` treats a request with a bearer header as a script and checks it against
`ADMIN_TOKEN`; otherwise it requires a session.

### Web UI sign-in (`service/auth.go`)

- **Checking a password.** If the email has a document in `users`, the password is checked
  against its bcrypt hash, and nothing else is accepted. Only when there is no document,
  and the email equals `ADMIN_EMAIL`, is the password compared (in constant time) with
  `ADMIN_PASSWORD`. Unknown emails still spend one bcrypt comparison, so response times
  don't reveal which accounts exist.
- **Changing the password** verifies the current password the same way, then upserts the
  `users` document with the new bcrypt hash. That document makes the environment password
  stop working for this email. All other sessions of the account are deleted; the current
  one stays.
- **Sessions.** Login creates a random 256-bit token. The browser gets it in an `HttpOnly`,
  `SameSite=Strict` cookie; MongoDB stores only its SHA-256 with the email and expiry.
  `Authenticate` rejects expired sessions itself; a TTL index removes them from the
  database. `SameSite=Strict` is the CSRF protection: browsers don't send the cookie on
  requests started by other sites.
- **Rate limit.** `POST /auth/login` allows 10 requests per minute per client IP
  (`go-chi/httprate`).

The frontend asks `GET /api/v1/auth/me` on load to learn whether it is signed in (the cookie
can't be read by scripts), and `RequireLogin` in `App.tsx` sends signed-out visitors to
`/login`.

## Recording lifecycle

One MongoDB document in `recordings` represents both the upload session and the recording;
its `_id` is the `uploadId` the device sees.

```
  Pocket webhook          pocket-fetch stage OK
  (none) ─────────▶ remote ─────────────────────┐
                                                 ▼
            create                 last byte, checksum + WAV OK        archive stage OK
  (none) ──────────▶ uploading ─────────────────────────────▶ received ──────────────▶ stored
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
| `stored` | FLAC (and optionally the WAV) in S3. The spool files are gone. |
| `failed` | Rejected WAV: nothing kept. Archive failure: the WAV stays in the spool for recovery. |

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

### The Pocket fetch stage (`service/pocket.go`)

`remote → received`: asks the Pocket API for a pre-signed download URL, streams the file
into `<id>.download` (limited to `MAX_UPLOAD_BYTES`), identifies the format from its first
bytes (`audio.Sniff`; pre-signed storage URLs often report a generic content type), and
records size, SHA-256 and media type. The Pocket API docs don't specify the field that
holds the URL, so `pocket.findURL` accepts a bare string or the usual field names; an
unrecognised response fails the stage with the response body in `lastError`.

### The archive stage (`service/archive.go`)

`received → stored`:

Fetched files that aren't WAV are uploaded unchanged to `recordings/pocket/<id>.<ext>`.
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

## Adding a processing stage

For example, transcription after archiving:

1. Add statuses in `domain/recording/recording.go`, e.g. `StatusTranscribed`, and fields
   for the results (e.g. `Transcript *Transcript`).
2. Implement the stage in `service`, e.g. `Transcriber.Run(ctx, rec)`. It can read the FLAC
   through `ports.ObjectStore` using `rec.Audio.Key`. Keep it idempotent.
3. Register it in `cmd/server/main.go` after the archive stage:

   ```go
   {Name: "transcribe", From: recording.StatusStored, To: recording.StatusTranscribed,
    Run: transcriber.Run},
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
| `name` | Label given at registration |
| `tokenHash` | SHA-256 of the token (unique index). Tokens are 256-bit random values, so an unsalted fast hash is enough. |
| `createdAt`, `lastSeenAt`, `revokedAt` | `lastSeenAt` is updated at most once a minute |

**`recordings`**

| Field | Notes |
|---|---|
| `_id` | Random 24-hex ID; the device's `uploadId` |
| `deviceId`, `clientId` | Owner and device-assigned `recordingId` (unique together) |
| `status` | `uploading`, `received`, `stored` or `failed` |
| `size`, `sha256` | Declared by the device at create |
| `recordedAt` | Optional, from the device |
| `format` | Sample rate, channels, bits, frames, duration. Set once received. |
| `audio`, `original` | S3 key, content type and size of the FLAC and the optional WAV |
| `attempts`, `notBefore`, `lastError` | Worker bookkeeping |
| `createdAt`, `updatedAt`, `receivedAt`, `storedAt` | Timestamps (UTC) |

Indexes: `(deviceId, clientId)` unique; `(status, notBefore)` for claiming;
`(status, updatedAt)` for stale uploads; `createdAt` for listing.

**`users`**: web UI accounts with a stored password. Empty until the admin changes the
default password.

| Field | Notes |
|---|---|
| `_id` | Random 24-hex ID |
| `email` | Lower-case (unique index) |
| `passwordHash` | bcrypt |
| `createdAt`, `passwordChangedAt` | Timestamps (UTC) |

**`sessions`**

| Field | Notes |
|---|---|
| `_id` | SHA-256 of the session token |
| `email` | The signed-in account (indexed, to end all its sessions) |
| `createdAt`, `expiresAt` | TTL index on `expiresAt` deletes expired sessions |

`repository/mongo/setup.go` creates collections and indexes on every start.

The `TxManager` in `repository/mongo/client.go` provides multi-document transactions (the
reason MongoDB runs as a replica set). Nothing uses it yet.

## Tests

| Test | Covers |
|---|---|
| `audio/*_test.go` | WAV parsing edge cases; FLAC output decodes to the exact input samples |
| `service/*_test.go` | Upload protocol: chunks, idempotency, offsets, dropped connections, checksum reset, invalid audio, isolation between devices, purge; device tokens; sign-in with the default login, password change overriding it, session expiry and logout |
| `worker/worker_test.go` | Archive stage end to end, retry/backoff, permanent failure, recovery |
| `transport/http/*_test.go` | Full HTTP flows: uploads, device and admin auth, browser sign-in with cookies, password change, login rate limit. `openapi_test.go` fails if a route under `/api/v1` is missing from `openapi.yaml` or the spec lists a route that doesn't exist. |
| `repository/mongo/repo_test.go` | Real MongoDB; runs only with `KNOWPOD_TEST_MONGO_URI` set |

The S3 store has no automated test. It has been checked by hand against an S3-compatible
server.
