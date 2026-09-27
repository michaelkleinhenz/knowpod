# Operations

Running knowpod-service in production. All settings are environment variables; the full
list is in the [README](../README.md#configuration).

## What it needs

| Dependency | Notes |
|---|---|
| MongoDB 7 | Replica set recommended (enables transactions). A standalone server works; the service logs a warning. |
| S3 bucket | An existing Amazon S3 bucket in any region. The service doesn't create it. |
| Persistent volume | Mounted at `UPLOAD_DIR` (`/data/uploads` in the image). |
| TLS | The service speaks plain HTTP. Put it behind a load balancer or reverse proxy that terminates HTTPS and sets `X-Forwarded-Proto: https`. Device tokens, admin tokens and web UI session cookies are bearer credentials. |

## Railway

The service is deployed on [Railway](https://railway.com). `railway.toml` in the repository
root sets the build (the root `Dockerfile`) and the deploy health check (`/healthz`).
Everything else is configured in the Railway dashboard:

1. **MongoDB.** Add a MongoDB service to the project.
2. **App service** from this repository. Railway picks up `railway.toml`.
3. **Volume.** Attach a volume to the app service with mount path `/data/uploads` (the
   `UPLOAD_DIR` of the image). Without it, uploads in progress and recordings not yet
   archived are lost on every redeploy.
4. **Variables** on the app service:

   | Variable | Value |
   |---|---|
   | `MONGO_URI` | `${{MongoDB.MONGO_URL}}` (reference to the MongoDB service's variable) |
   | `MONGO_DATABASE` | `knowpod` |
   | `ADMIN_EMAIL`, `ADMIN_PASSWORD` | The default web UI login |
   | `AWS_S3_BUCKET_NAME`, `AWS_DEFAULT_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | The S3 bucket and an IAM user's keys (see [S3](#s3)) |
   | `RAILWAY_RUN_UID` | `0` (see below) |
   | `ADMIN_TOKEN` | Optional, for scripts |

   Don't set `PORT`; Railway provides it and the service listens on it.
5. **Networking.** Generate a public domain for the app service. Railway terminates HTTPS
   and sets `X-Forwarded-Proto` and the client IP headers that the session cookie and the
   login rate limit rely on.

**Why `RAILWAY_RUN_UID=0`:** Railway mounts volumes owned by root, and the image runs as the
unprivileged user `app` (uid 10001). Without the variable, the service can't write to
`/data/uploads`: uploads fail with permission errors.

**One replica.** Keep the app service at one replica; the spool is local to the instance
(see [Scaling](#scaling-and-the-spool)). A Railway service with a volume is limited to one
replica anyway.

If Railway's MongoDB runs as a standalone server rather than a replica set, the service
logs `MongoDB is not a replica set member` at startup. That's fine; nothing uses
transactions yet.

## Startup checks

On start the service validates the configuration, connects to MongoDB and creates any
missing collections and indexes, checks that the bucket is reachable (`HeadBucket`), and
creates the spool directory. If any of these fails it logs the reason and exits with
status 1, so misconfiguration shows up at deploy time.

## S3

**IAM policy** for the service's identity (instance role, task role or access keys):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket"],
      "Resource": "arn:aws:s3:::knowpod-audio"
    },
    {
      "Effect": "Allow",
      "Action": ["s3:PutObject", "s3:GetObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::knowpod-audio/*"
    }
  ]
}
```

`s3:ListBucket` is needed for the startup check. If you set `AWS_S3_PREFIX`, you can narrow the
object resource to `arn:aws:s3:::knowpod-audio/<prefix>*`.

**Credentials** are resolved by the AWS SDK's default chain. That includes the environment
variables `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN`, `AWS_PROFILE`,
web identity (EKS), and ECS/EC2 roles. The region must be set with `AWS_DEFAULT_REGION` (or
`AWS_REGION`, which takes precedence).

**Bucket settings** (recommended): block all public access, keep default encryption on, and
consider a lifecycle rule that moves older recordings to a cheaper storage class. The
service only reads objects when an admin downloads them.

## Scaling and the spool

The spool holds uploads in progress and received recordings not yet archived. Normally it
stays small. If S3 is unreachable, received recordings pile up there until archiving
succeeds or gives up, so size the volume for the audio you might receive during an outage.

Run **one instance**. Upload chunks go to local disk and appends are coordinated
in-process, so several instances behind a plain load balancer would split one upload
across disks. Scaling out would need routing each device to a fixed instance with its own
volume, or moving the spool to shared storage.

Long uploads mean long requests. Make sure the proxy in front allows large request bodies
and doesn't time out slow uploads too early. The protocol resumes after a cut connection,
but every cut costs a round trip.

## Users and sign-in

People sign in to the web UI with email and password. Every recording and device belongs to
a user, and each user sees only their own notes and devices.

| Role | Can |
|---|---|
| **User** | Use Notes (including uploads and summary details), Devices, Status, Account (own password, own Pocket integration, own reMarkable link) and Settings (language, own themes). |
| **Admin** | Everything a user can, plus **Users** (create, edit, set passwords, delete) and the OpenRouter section of **Settings**. |

**The built-in admin** is `ADMIN_EMAIL`. Its record is created at startup; until a password
is set for it in the UI (by itself under **Account**, or by another admin under **Users**),
its password is `ADMIN_PASSWORD` from the environment. After that, the stored password
applies and `ADMIN_PASSWORD` no longer works. The built-in admin can't be deleted or
demoted, so there is always a way in; it also owns everything created before there were
users (assigned once at startup).

**Managing users** (admins, **Users** page): create a user with an initial password and a
role, change a user's email or role, set a new password (the user is signed out
everywhere), or delete a user. Deleting removes the user **with all their devices,
notes, audio and themes**. Admins can't delete themselves, and the last admin can't be
removed or demoted.

**Sessions** are stored in MongoDB and last `SESSION_TTL` (7 days by default). The user is
looked up on every request, so role changes and deletions take effect immediately. The
cookie is `HttpOnly` and `SameSite=Strict`, and `Secure` when the request came in over HTTPS
(directly, or with `X-Forwarded-Proto: https` from the proxy). Login attempts are limited to
10 per minute per client IP.

**Forgotten password.** Another admin sets a new one under **Users**. For the built-in admin
without any other admin, clear its stored password; `ADMIN_PASSWORD` then works again:

```js
db.users.updateOne({ email: "admin@example.com" }, { $set: { passwordHash: "" } })
```

**Changing `ADMIN_EMAIL`** creates a new built-in admin at the next start. The old account
stays as a normal admin with its stored password; delete it under **Users** if it shouldn't.

## Pocket integration

Each user can connect their own [Pocket](https://heypocket.com) recorder. Pocket calls the
user's personal webhook whenever something happens to a recording; knowpod then downloads
the audio with that user's Pocket API key and archives it in S3 like the other recordings.
The recordings belong to that user.

**Setup** (each user, **Account** page → Pocket integration):

1. Copy **Your webhook URL** (`https://<your domain>/api/v1/webhooks/pocket/<random id>`).
   The Status page shows it too.
2. In the Pocket app's integrations settings, add a webhook with that URL. Pocket shows the
   webhook's **signing secret** once; paste it into **Webhook signing secret**.
3. Create a Pocket **API key** (`pk_…`), paste it into **Pocket API key**, and save.

Until both are set, the user's webhook answers `503` and nothing is stored. **Disconnect**
removes both. The secret and key are stored with the user in MongoDB and are never shown
again (only the key's last four characters).

**What happens**

- The webhook URL identifies the user; the request's signature (`X-HeyPocket-Signature`,
  HMAC-SHA256 over `<X-HeyPocket-Timestamp>.<body>`) must verify with that user's secret,
  and its timestamp must be within 5 minutes of the server clock. Unknown URLs get `404`,
  unsigned or stale requests `401`.
- Every event that names a recording (`recording.created`, `transcription.completed`,
  `summary.completed`, …) queues that recording **once** per user; Pocket delivers at least
  once and later events for the same recording are ignored. `recording.deleted` is ignored:
  the archived copy is kept.
- The worker asks the Pocket API for a download URL
  (`GET /public/recordings/{id}/audio-url`) with the owner's API key, streams the file to
  the spool, and archives it: WAV is transcoded to FLAC, other formats (e.g. MP3, M4A) are
  stored as they are. Failures, including audio that isn't available yet, are retried with
  backoff and end in `failed` after `WORKER_MAX_ATTEMPTS`.
- Pocket recordings have `source` `pocket`, `deviceId` `pocket:<userId>`, the Pocket
  `title`, and the Pocket recording ID as `recordingId`.

Pocket's transcripts, summaries and action items in the webhook payload are not stored.

**Log messages:** `pocket recording queued`, `pocket audio fetched`, and
`pocket webhook rejected` (signature problems, with the reason and user).

## reMarkable

Each user can pair their own reMarkable cloud account. knowpod then imports all documents
of the account (except those in the trash) as notes into the knowpod folder **reMarkable**,
which is created with the first import. It only
reads: nothing on the tablet or in the cloud is changed, moved or deleted.

**Setup** (each user, **Account** page → reMarkable):

1. Get a one-time code at
   [my.remarkable.com/device/browser/connect](https://my.remarkable.com/device/browser/connect).
2. Enter the 8-character code and press **Pair**. The first import starts right away.

Pairing registers knowpod as a device of the reMarkable account and stores its device
token in the `tablets` collection; the token is never shown. **Unpair** forgets the token;
the device stays listed under "Connected devices" at my.remarkable.com until removed there
(removing it there makes the next import fail with "pair again").

**What happens**

- Every `REMARKABLE_PULL_INTERVAL` (default 15 minutes), and on **Import now**, knowpod
  reads the account's root. When nothing changed, that costs two requests; otherwise only
  the changed documents' metadata is read again.
- Each document becomes one note (`type` `document`, `source` `remarkable`,
  `deviceId` `remarkable:<userId>`, the document's ID as `recordingId`, its name as
  `title`). The pipeline then downloads the document's files, stores the PDF (notebooks are
  rendered to a vector PDF from their strokes; PDFs and EPUBs are kept as they are), and a
  vision model reads the pages into Markdown, which is summarized like a transcript.
- When a document's content changes, the note is queued again (a rename only changes its
  `title`). A summary the user edited is kept; **Read again** on the note replaces it.
- Notes stay when documents are deleted on the reMarkable. The knowpod folder can be renamed
  or moved; notes moved out of it stay where they are put.

**Log messages:** `remarkable paired`, `reMarkable documents queued`,
`reMarkable document fetched`, `reMarkable document stored`, `document read`, and
`reMarkable pull failed` (with the reason and user).

## Summaries: themes, language, model

Every summary is written with a **theme**, a set of instructions that define its structure.
Built-in themes: Auto (adaptive; the default), Meeting Notes, Call Notes, Interview Notes,
Dictation Notes, Key Points and Lecture Notes. Under **Settings → Themes** each user can:

- **edit a built-in theme** (name, description, instructions). The change applies to that
  user only; the theme is marked "Customized" and **Reset to default** restores it;
- **add own themes** (name, short description, instructions such as "Write `## Needs` as
  bullets, then `## Next steps` with owners").

Customized and own themes are private to the user and deleted with the user.

A note's actions are icons at the right of the Summary / Transcript / Source switcher:
**Summary details** (sliders; summary tab only), **Download** and **Copy** (for the shown
tab: the summary as Markdown, the transcript as text, or the audio file), **Re-transcribe**
and **Delete**.

**Summary details** sets:

- **Language**: auto-detect (the transcript's language; default) or one of 23 languages.
- **Model**: the default summary model from the admin settings, or any OpenRouter text model.
- **Theme**: a built-in or own theme.

**Regenerate summary** stores these choices with the recording (they also apply to later
re-summaries and re-transcriptions) and summarizes it again.

**Layout.** On desktop, the notes list stays in a sidebar on the left and the open note is
shown next to it; on phones the list and a note are separate screens.

**Editing.** A note's summary is always editable, like a document in a word processor:
clicking into the text places the cursor there, and the title at the top of the page is
edited in place. The note is a plain white page without a toolbar:

- Typing **/** at the start of an empty line opens a block menu: headings 1–3, bulleted,
  numbered and task lists, code block, quote and divider. Keep typing to filter it, choose
  with the arrow keys and Enter (or a click), close it with Escape.
- **Selecting text** shows a small bubble with bold, italic, strikethrough, code and link.
- Markdown shortcuts work too (`## ` for a heading, `- ` for a list, `[ ] ` for a task,
  `**bold**`), lists nest with Tab, Ctrl/Cmd+Z undoes, Ctrl/Cmd+S saves immediately and
  Ctrl/Cmd+click opens a link.
 Summaries are stored as
Markdown; the editor reads and writes Markdown, so nothing else changes. Edited summaries
show "Edited <date>"; just viewing a note never changes it.

Changes are **saved automatically** 2 seconds after typing stops, and at least every 10
seconds while typing continues. The sync state is a colored dot at the far right of the
note's toolbar: green (*All changes saved*), amber (*Unsaved changes*), pulsing (*Saving…*),
or red (*Not saved* / *Offline*; click it to retry). Hover it for the words. Failed saves are retried every 10 seconds and as soon as the browser is online again;
leaving the note saves what's left, and closing the tab with unsaved changes asks first.

**Regenerating asks first.** Regenerate summary and Re-transcribe always ask
for confirmation (with an explicit warning when the summary was edited or has unsaved
changes). After confirming, pending changes are dropped rather than saved over the new
summary.

## Settings

**Settings** is organized in tabs: **General** (app language), **Themes** (built-in and own
summary themes) and, for admins, **AI processing** (OpenRouter). The open tab is part of the
URL (e.g. `/settings?tab=themes`).

## Language of the app

The web UI is available in English and German. Before signing in it follows the browser's
language; each user then chooses a language under **Settings → General**, which is saved
with the account and applies on every device. API error responses carry a stable `code`
(e.g. `invalid_login`, `email_taken`) that the UI translates; the `error` text stays
English. The API reference on the Status page is shown in English.

## Highlights and downloads

**Highlights.** Recorders can send the moments the user marked while recording (see
[Device upload protocol](device-protocol.md#highlights)). They are stored with the note,
shown on the **Source** tab as markers on a timeline and as a list (click to play from
there), and passed to the summarizer, which adds a "Highlights" section describing what was
said at each moment. Transcripts carry `[m:ss]` time stamps per speaker turn for this; in
the web app they are clickable and play the audio from that point.

**Downloads.** In the web app, the **Download** icon saves the summary (Markdown), the
transcript (text) or the audio, depending on the shown tab. Through the API (session or `ADMIN_TOKEN`):

```bash
curl -H "Authorization: Bearer $ADMIN_TOKEN" -OJ $API/recordings/<id>/summary      # "<title> - summary.md"
curl -H "Authorization: Bearer $ADMIN_TOKEN" -OJ $API/recordings/<id>/transcript   # "<title> - transcript.txt"
curl -H "Authorization: Bearer $ADMIN_TOKEN" "$API/recordings/<id>/summary?format=json"
```

## Uploading audio files

On **Notes**, **Upload** (or dragging files onto the page) sends WAV and MP3 files
from the browser; several at a time are fine, each with a progress bar. The format is
detected from the file's content: WAV must be integer PCM (like device uploads) and is
archived as FLAC, MP3 is archived as it is; other formats are refused. The file name becomes
the title until the summary provides one, and the file's modification time is used as the
recording time. The size limit is `MAX_UPLOAD_BYTES`; the proxy in front must allow bodies
that large.

## Installing the app

The web UI is an installable web app (PWA): in Chrome/Edge use **Install app** in the
address bar or menu, on Android **Add to home screen**, on iOS Safari **Share → Add to Home
Screen**. It then opens in its own window without browser controls. The app shell is cached
by a service worker so it starts instantly; notes and all other data are always
loaded live (API responses are never cached), so the app needs a connection to show
content. New versions are picked up automatically on the next start. Installing requires
HTTPS (or `localhost`).

## AI processing (OpenRouter)

Every archived recording is transcribed and then summarized through
[OpenRouter](https://openrouter.ai). This is configured in the web UI, not with environment
variables:

1. Create an API key at [openrouter.ai/settings/keys](https://openrouter.ai/settings/keys)
   (and add credit to the OpenRouter account).
2. In knowpod, open **Settings** (as an admin), paste the key under **AI processing**,
   choose a **transcription model** (only models that accept audio are offered) and a
   **default summary model**, and save. Users can pick another summary model per note.
   Optionally choose a **document model** for reading reMarkable documents (only models that
   accept images are offered); without one, the transcription model reads them, so pick a
   transcription model that also takes images (e.g. `google/gemini-2.5-flash`) or set one.

Recordings that arrived before this wait in `stored` and are processed as soon as the
settings are saved. Usage is billed by OpenRouter per token; the Settings page shows each
model's price per million tokens.

**How audio is sent.** OpenRouter takes audio base64-encoded inside the request. FLAC
recordings (all device uploads) are decoded, mixed to mono, reduced to 16 kHz and sent in
5-minute WAV pieces, whose transcripts are joined. Recordings kept in another format
(MP3 from Pocket or browser uploads, M4A from Pocket) are sent in one piece and are limited to 20 MB (roughly 40 minutes of
MP3 at 64 kbit/s); larger ones fail with a clear error.

**How documents are sent.** Notebook pages with writing are rendered to PNG images
(1053×1404 pixels) and sent eight at a time; PDFs are sent as files (up to 20 MB). At most
50 pages of a document are read; the text says when pages were left out. EPUBs are not read.

**The API key** is stored in the MongoDB `settings` collection. It is never sent back to the
browser (the UI shows only its last four characters), but anyone with database access can
read it; protect database backups accordingly, and remove the key in Settings if it leaks.

**When a step fails**, it is retried like the other stages and the recording ends in
`failed` after `WORKER_MAX_ATTEMPTS`, with the reason shown on the note's page. Fix the
cause (credit, model choice) and use **Re-transcribe** or **Summary details → Regenerate summary** there.

Log messages: `recording transcribed` (model, length, duration) and `recording summarized`
(model, title).

## Provisioning devices

Each user manages their own recorders on the **Devices** page; uploads from a device belong
to its owner.

- **Add device:** enter a name. The device's API token appears in its row with a copy
  button. Configure it on the recorder right away: the token is shown only once and is gone
  when you leave the page (only its hash is stored).
- **New token:** issues a replacement token, e.g. when the old one was lost. The old token
  stops working immediately; the device keeps its recordings.
- **Remove:** revokes the device. Its token stops working and it disappears from the list;
  recordings it already uploaded are kept.

**From scripts**, use the API with `ADMIN_TOKEN` as a bearer token. Scripts act as the
built-in admin (devices they create belong to it) and see all users' devices and
recordings. `ADMIN_TOKEN` is optional; set it to a long random value, e.g.
`openssl rand -base64 32`. When it's empty, only signed-in web UI sessions can use the API.

```bash
API=https://knowpod.example.com/api/v1
ADMIN="Authorization: Bearer $ADMIN_TOKEN"

# Register a device. The token is returned only once; put it on the gadget.
curl -s -X POST -H "$ADMIN" -d '{"name":"recorder-kitchen"}' $API/devices

# List devices (shows ownerId, lastSeenAt and revokedAt).
curl -s -H "$ADMIN" $API/devices

# Issue a new token for a device (the old one stops working).
curl -s -X POST -H "$ADMIN" $API/devices/<deviceId>/token

# Remove (revoke) a device. Its recordings are kept.
curl -s -X DELETE -H "$ADMIN" $API/devices/<deviceId>
```

## Monitoring

- **`GET /healthz`** returns `200` when MongoDB answers and `503` otherwise. It doesn't
  check S3. The **Status** page in the web UI shows the same result.
- **Logs** are JSON lines on stdout. Useful messages:

  | Message | Meaning |
  |---|---|
  | `recording archived` | A recording reached `stored` (includes WAV and FLAC sizes and processing time) |
  | `stage failed, will retry` | Archiving failed and will be retried; see `err` |
  | `stage failed permanently` | A recording became `failed` after all attempts |
  | `purged stale uploads` | Abandoned uploads were deleted |
  | `request failed` | Unexpected error behind a `500` response |

- **Recording state:** `GET /api/v1/recordings?status=failed` with `ADMIN_TOKEN` lists
  every user's failed recordings with their `lastError`. A growing number of `received` recordings means
  archiving is falling behind or failing.

## Recovering failed recordings

A recording can fail for two reasons, which `lastError` distinguishes:

- **The device sent something that isn't a supported WAV.** Nothing was kept, so there is
  nothing to recover on the server.
- **Archiving failed** (e.g. S3 was unreachable longer than the retries lasted). The WAV is
  still in the spool as `<id>.wav`, where `<id>` is the recording's `id` (the device's
  `uploadId`, not its `recordingId`). Once the cause is fixed, put the recording back in the
  queue with `mongosh`:

  ```js
  db.recordings.updateOne(
    { _id: "<id>", status: "failed" },
    { $set: { status: "received", attempts: 0, notBefore: new Date() }, $unset: { lastError: "" } }
  )
  ```

  The worker picks it up within `WORKER_POLL_INTERVAL`.

## Backups

MongoDB holds the metadata (users with password hashes and Pocket secrets, devices,
recording states, transcripts, summaries, S3 keys) and the OpenRouter key; back it up as
usual and treat backups as sensitive. The
audio lives in S3, where you can enable versioning or replication. The spool is only a
transit area, but it does contain received recordings until they are archived, so don't
wipe it while `received` recordings exist.

## Known limitations

- Single instance only (see [Scaling](#scaling-and-the-spool)).
- Users can't reset their own forgotten password; an admin sets a new one.
- Admins see only their own notes in the UI; `ADMIN_TOKEN` scripts see everyone's.
- Pocket or other compressed audio above 20 MB can't be transcribed (it is sent in one
  piece; splitting it would need an MP3/AAC decoder).
- Speaker labels ("Speaker 1") are assigned per 5-minute piece and may not match across
  pieces of long recordings.
- The notes list loads the newest 200 recordings.
- The client IP for the login rate limit is taken from `X-Forwarded-For` / `X-Real-IP`.
  Without a proxy that sets these, clients can spoof them and bypass the limit.
- No API to delete recordings or their objects.
- No retry endpoint for failed recordings; use the `mongosh` update above.
- `/healthz` doesn't cover S3.
- reMarkable: typed text in notebooks (Type Folio, text boxes) is not rendered or read, and
  handwritten annotations on PDFs and EPUBs are not included in the stored file. Page
  templates (lines, grids) are not drawn. Very long notebook pages are scaled down for the
  model.
- reMarkable: the cloud API is not documented by reMarkable; the client follows the protocol
  used by [rmapi](https://github.com/ddvk/rmapi) and may break when reMarkable changes it.
- FLAC compression is weaker than the reference encoder (see
  [Architecture](architecture.md#flac-encoding-audioflacgo)).
