# knowpod-service

knowpod is a universal note taking and todo management app that brings everything you
capture into one place, and the service behind it. It integrates with your productivity
tools and gadgets: conversations recorded on AI audio recorders arrive transcribed and
summarized, handwritten reMarkable notebooks arrive as searchable text, and everything else
you write or plan lives next to them as notes, tasks and boards. Use it in the browser, as
an installed app on your phone, or as a desktop app on Windows, macOS and Linux.

A Go backend with an embedded React web UI, built and shipped as a single binary. Metadata
lives in MongoDB, files in S3, and AI models are reached through OpenRouter.

## What it does

- **Notes of every kind.** Markdown text notes, transcribed and summarized recordings,
  imported documents and kanban boards, organized in folders, sub-notes and labels, linked
  to each other by number, searchable, and available offline.
- **Todo management.** Any note can be a task with a due date, time, repeat rule, priority
  and reminder, typed in plain English or German ("Call Anna tomorrow 3pm p1"). Action items
  from your conversations become tasks in one click, and reminders arrive as push
  notifications on your phone and computer.
- **Integrations with your tools and gadgets.**

  | Source | What arrives in knowpod |
  |---|---|
  | knowpod recorders and other gadgets | Recordings over a resumable [upload API](docs/device-protocol.md), transcribed and summarized, with highlights |
  | [Pocket](https://heypocket.com) recorders | Recordings through a personal webhook |
  | [reMarkable](https://remarkable.com) tablets | Notebooks, PDFs and EPUBs, with handwriting read into text |
  | Audio files | WAV and MP3 uploads from the browser |
  | AI models ([OpenRouter](https://openrouter.ai)) | Transcripts, titles, summaries and action items |
  | Scripts and your own tools | The full [REST API](backend/api/openapi.yaml), downloads as Markdown and text |

- **Everywhere you work.** A responsive web app, installable on phones and desktops (PWA),
  and a native [desktop app](#desktop-app); English and German.

## Tech stack

| Layer | Technology |
|---|---|
| Backend | Go 1.24, chi router, official MongoDB driver, AWS SDK v2 |
| Database | MongoDB 7 as a single-node replica set (for multi-document transactions) |
| File storage | Amazon S3 (an existing bucket) for audio and documents |
| Transcoding | Pure-Go FLAC encoder ([mewkiz/flac](https://github.com/mewkiz/flac)), no external binaries |
| Frontend | React 18 + TypeScript, Vite, react-router |
| Deploy | One binary (frontend embedded in the backend), Docker / docker-compose |
| Desktop app | Electron + electron-builder (Windows, macOS, Linux), optional |

## Features

- **Users** sign in with email and password and each see only their own notes and
  devices. Admins manage users and the AI settings. The built-in admin is `ADMIN_EMAIL`,
  whose password is `ADMIN_PASSWORD` until one is set in the UI.
- Each gadget authenticates with its own revocable token, created on the web UI's
  **Settings → Devices** tab.
- Each user can connect their own [Pocket](https://heypocket.com) recorder on the
  **Settings → Account** tab: recordings arrive through the user's personal webhook and their audio is
  downloaded with the user's Pocket API key.
- WAV and MP3 files can be uploaded from the browser on **Notes**.
- Each user can pair their **reMarkable** cloud account under **Settings → Account** with a
  one-time code. All its documents (except the trash) are imported into the knowpod
  folder **reMarkable**, in the same folders as on the tablet (read only, never changed on the tablet): handwritten notebooks are rendered to PDF, PDFs and EPUBs are kept as they
  are, and a vision model reads the pages into text that is summarized like a transcript.
- Notes come in types: **audio** notes (recordings, transcribed and summarized),
  **text** notes, plain Markdown documents written in the browser (**+** on **Notes**), and
  **documents** from the reMarkable. The
  notes list shows each note's type as an icon; text notes are edited, copied, downloaded
  and deleted like summaries.
- Every note has a **number** of its own (#1, #2, … per user, never reused). Typing **#** in
  a note's text opens a list of your notes, filtered by number (or title) as you type; the
  chosen note is linked as "#12" (Ctrl/⌘+click opens it). Search also finds notes by number.
- **Boards** are kanban boards, listed like any other note: each shows the notes of a folder or
  with a label as cards, all starting in the first column. New boards have the columns Todo,
  In Progress and Done; columns can be renamed, added and deleted, and cards are dragged
  between them.
- A note's page shows its title, then one compact row with its number, date, labels and
  icon actions; a dot in the top right corner shows whether its edits are saved. On wide
  screens a sidebar next to the note (not on boards) holds its icon actions, task (check
  box, date, priority), labels and details (date, type, duration, folder, boards, status,
  model), and the header keeps only the title and number; on narrower screens they stay in
  the header.
- Notes can carry **labels**: colored chips on the note's page, with your own labels
  defined on the spot or under **Settings → Labels**. The built-in **Task** label adds a
  check box to the note's icon in the list; the check mark is saved.
- **Tasks** have a due date, an optional time, a repeat rule ("every weekday", "every 2
  weeks"), a reminder and a priority (P1–P3), set from the **Date** button on the note's
  page. Dates can be typed in English or German ("tomorrow 3pm", "jeden Montag", "am
  5.10."), also in a note's title. The **Tasks** view lists the open tasks by due date and
  adds new ones from one line ("Call Anna tomorrow 3pm p1"); checking off a recurring task
  moves it to its next date.
- Summaries list the **action items** found in the conversation; each becomes a task under
  the note with one click, due on the date that was named.
- **Reminders** arrive as push notifications in every browser or installed app they are
  turned on in (**Settings → Account → Notifications**), on phones too (on iPhone: from the Home Screen
  app).
- The notes list shows notes **by time** (grouped by day) or in **folders**, like files.
  Folders can be nested, renamed and deleted (their notes move up, nothing is lost); drag
  notes and folders onto a folder to move them, or use the note's **Move to folder** button.
- Notes can hold **sub-notes**, like a folder whose head is a note itself: **New sub-note**
  below a note creates one, and dropping a note onto another note in the folder view moves
  it under that note. Sub-notes open and close under their parent, in the folder view and in
  the note's own sub-notes list (Alt+click opens or closes a whole tree; opening a note
  unfolds the notes above it in the sidebar). The note's header shows the notes above it, and deleting a note moves its sub-notes up (nothing
  else is lost). Moving a sub-note into a folder takes it out from under its parent.
- Deleted notes go to the **Trash**, the last folder in the folder view (dropping a note
  onto it deletes it too). They stay there for 14 days and can be restored from the note's
  page; after that they are deleted for good. **Delete for good** and **Empty trash** don't
  wait.
- The web app works **offline**: it is installable and keeps a copy of every note in the
  list (text, summaries, transcripts, labels and folders) in the browser, synced in the
  background whenever it is online. Without a connection the header shows **Offline** and the
  notes are shown as last synced; edits to an open note are saved once the connection is
  back. Audio and document files need a connection. Signing out removes the copies.
- Recorders can send **highlights** (moments marked with a button while recording); they
  are shown on the note's timeline and described in the summary.
- Summaries and transcripts can be downloaded as Markdown and text files, in the web app and
  through the API.
- Uploads are idempotent (the gadget names each recording), resumable after dropped
  connections, and checked against a SHA-256 the gadget declares up front.
- Transcoding and archiving run in a background worker with retries and backoff.
- Every archived recording is transcribed and summarized through
  [OpenRouter](https://openrouter.ai). The API key and both models are chosen by an admin
  in the web UI under **Admin → General**.
- The web UI's **Notes** page (a note per recording) lists your recordings by the title of
  their summary; on desktop the list stays in a sidebar next to the open note;
  each note shows its summary, transcript and audio, and can be re-transcribed,
  re-summarized (via Summary details) or deleted from icon buttons next to the view switcher. Summaries are always editable in place, like a document
  (type **/** for headings, lists and tasks; select text to format it), and save
  automatically (stored as Markdown). Under **Summary details** each summary's language, model and
  **theme** (its structure, e.g. meeting or call notes) can be changed and regenerated.
  Users adjust the built-in themes and add their own on the **Settings** page.
- The web UI is available in English and German; each user picks the language in
  **Settings**.
- The web UI works on phones and can be installed as an app (PWA).
- A **desktop app** for Windows, macOS and Linux is an alternative to the browser: the same
  web UI in a native window, signed in to and talking to the server exactly like the web app
  (see [Desktop app](#desktop-app)).

## How recordings flow

Recordings from gadgets go through a pipeline before they appear as notes:

```
gadget ──POST /uploads──▶ upload created (status: uploading)
       ──PATCH chunks──▶ streamed to the local spool (UPLOAD_DIR), resumable
                         last byte: SHA-256 + WAV header verified (status: received)
background worker ─────▶ WAV → FLAC, uploaded to S3 (status: stored), spool cleaned up
AI worker ─────────────▶ transcript (status: transcribed) → title + summary (status: summarized)
```

Supported gadget input: integer PCM WAV, 8/16/24 bit, 1–8 channels, up to 4 GiB.

## Documentation

| Document | For |
|---|---|
| [Device upload protocol](docs/device-protocol.md) | Implementing the upload client on the gadget: requests, error handling, retry logic |
| [Architecture](docs/architecture.md) | Backend developers: components, recording lifecycle, worker, data model, adding processing stages |
| [Operations](docs/operations.md) | Deploying and running: Railway, AWS/IAM setup, users and sign-in, Pocket, reMarkable, uploads, installing the app, desktop app, AI settings, devices, monitoring, recovery, limitations |
| [OpenAPI spec](backend/api/openapi.yaml) | The formal API definition. The service serves it at `/api/v1/openapi.yaml` and `/api/v1/openapi.json`, and the Devices tab links to it. |

## Quick start (Docker)

You need an existing S3 bucket and AWS credentials that can use it (see
[Operations](docs/operations.md#s3)).

```bash
cp .env.example .env      # fill in ADMIN_PASSWORD, the bucket, region and AWS credentials
docker compose up --build
```

- Web UI: <http://localhost:8080>. Sign in with `ADMIN_EMAIL` / `ADMIN_PASSWORD`, then
  change the password under **Settings → Account**.
- API: under `/api/v1`. The health check at `/healthz` also checks database connectivity.
- MongoDB: `localhost:27017`

### Try it with curl

Scripts use the API with `ADMIN_TOKEN` (set it in `.env`) instead of a browser session;
they act as the built-in admin.

```bash
set -a && . ./.env && set +a       # load ADMIN_TOKEN into the shell
API=http://localhost:8080/api/v1
ADMIN="Authorization: Bearer $ADMIN_TOKEN"

# Register a device; keep the token, it is shown only once.
TOKEN=$(curl -s -X POST -H "$ADMIN" -d '{"name":"test-recorder"}' $API/devices | jq -r .token)

# Upload a WAV file.
SIZE=$(stat -c %s rec.wav); SHA=$(sha256sum rec.wav | cut -d' ' -f1)
ID=$(curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -d "{\"recordingId\":\"rec-001\",\"size\":$SIZE,\"sha256\":\"$SHA\"}" $API/uploads | jq -r .uploadId)
curl -s -X PATCH -H "Authorization: Bearer $TOKEN" -H "Upload-Offset: 0" \
  --data-binary @rec.wav $API/uploads/$ID

# Inspect it and fetch the archived FLAC.
curl -s -H "$ADMIN" $API/recordings/$ID | jq
curl -s -H "$ADMIN" -o rec.flac $API/recordings/$ID/audio
```

## Deploying

Production runs on [Railway](https://railway.com) with the root `Dockerfile` and
`railway.toml`. The setup (MongoDB, volume, variables) is described in
[Operations](docs/operations.md#railway).

## Build the binary without Docker

```bash
make build
./backend/bin/server      # requires MongoDB and an S3 bucket (see Configuration)
```

## Desktop app

`desktop/` holds an Electron app: a native window around the web UI. It contains no backend
and no copy of the frontend; it loads the web app from a knowpod server and uses the API
exactly as the browser does (same session cookie, same offline copies). On the first start
it asks for the server's address (**File → Change Server…** changes it later).

```bash
make desktop-run SERVER_URL=http://localhost:8080   # start it from source against a server
make desktop                                        # installers for this OS, in desktop/dist
make desktop-linux                                  # AppImage, .deb, .tar.gz
make desktop-windows                                # NSIS installer (needs Windows or Wine), .zip
make desktop-mac                                    # .dmg, .zip (needs macOS)
make desktop SERVER_URL=https://knowpod.example.com # preset the server, no question on first start
```

Requires Node.js 22. Build each platform on its own OS; the **Desktop app** GitHub Actions
workflow (`.github/workflows/desktop.yml`, run by hand or on a `desktop-v*` tag) builds all
three and keeps the installers as artifacts. The builds are not code-signed, so macOS
Gatekeeper and Windows SmartScreen warn on first open. The desktop app can't receive push
notifications (see [Operations](docs/operations.md#desktop-app)).

## Local development

The Vite dev server gives hot reload and proxies `/api` to the backend, so no re-embedding
is needed. Start MongoDB from compose and run the backend against it with your `.env`:

```bash
docker compose up -d mongo
cd backend && set -a && . ../.env && set +a && go run ./cmd/server
cd frontend && npm install && npm run dev   # http://localhost:5173, proxies /api to :8080
```

## Configuration

Environment variables only.

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `MONGO_URI` | `mongodb://localhost:27017/?replicaSet=rs0` | MongoDB connection string |
| `MONGO_DATABASE` | `knowpod` | Database name |
| `FRONTEND_URL` | `http://localhost:5173` | Allowed CORS origin |
| `ADMIN_EMAIL` | _(empty)_ | Email of the built-in admin (created at startup) |
| `ADMIN_PASSWORD` | _(empty)_ | The built-in admin's password until one is set in the UI |
| `SESSION_TTL` | `168h` | How long a web UI sign-in lasts |
| `ADMIN_TOKEN` | _(empty: disabled)_ | Bearer token for scripts: the whole API (except the device upload API) as the built-in admin, seeing all users' data |
| `POCKET_API_URL` | `https://public.heypocketai.com/api/v1` | Pocket API base URL |
| `REMARKABLE_PULL_INTERVAL` | `15m` | How often paired reMarkable accounts are checked for new and changed documents; `0` turns the automatic import off (the **Import now** button still works) |
| `WEBPUSH_SUBJECT` | `mailto:` + `ADMIN_EMAIL` | Contact (`mailto:` or `https:` URL) sent to browser push services with notifications |
| `REMARKABLE_AUTH_URL`, `REMARKABLE_SYNC_URL` | _(empty: the public reMarkable cloud)_ | reMarkable cloud endpoints, e.g. for a self-hosted compatible server |
| `AWS_S3_BUCKET_NAME` | _(required)_ | Existing bucket for the audio files |
| `AWS_S3_PREFIX` | _(empty)_ | Key prefix inside the bucket |
| `AWS_DEFAULT_REGION` | _(required)_ | AWS region of the bucket. `AWS_REGION` also works and takes precedence. |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_PROFILE`, … | | Standard AWS SDK credentials; instance/task roles work too |
| `UPLOAD_DIR` | `./data/uploads` (`/data/uploads` in the image) | Local spool for uploads |
| `MAX_UPLOAD_BYTES` | `4294967296` | Largest accepted WAV file |
| `UPLOAD_TTL` | `48h` | Incomplete uploads idle longer than this are purged |
| `KEEP_ORIGINAL_WAV` | `false` | Also archive the original WAV next to the FLAC |
| `WORKER_POLL_INTERVAL` | `10s` | How often the worker checks for due work |
| `WORKER_MAX_ATTEMPTS` | `5` | Attempts per processing stage before `failed` |

Objects are stored at `recordings/<userId>/<id>.flac` (and `.wav` when kept), where `<id>`
is the server-assigned recording ID. Audio kept in its original format (MP3 or M4A from
Pocket or browser uploads) is stored as `recordings/<userId>/<id>.<ext>`; a reMarkable
document's PDF or EPUB as `recordings/<userId>/<id>.pdf` (`.epub`) and its original files as
`recordings/<userId>/<id>.rmdoc` (a zip). Recordings
archived before users existed keep their earlier keys. For the required IAM permissions, see
[Operations](docs/operations.md#s3).

## Layout

```
backend/
  api/openapi.yaml     API specification
  cmd/server/          entrypoint and wiring
  internal/
    audio/             WAV parsing, WAV → FLAC, format sniffing, speech chunks for transcription
    config/            environment-based configuration
    domain/            models: recording (lifecycle, owner), device, user (role, Pocket) + session,
                       tablet (reMarkable link)
    openrouter/        OpenRouter API client (chat completions with audio, model list)
    pocket/            Pocket webhook signatures and API client
    remarkable/        reMarkable cloud client (read only), page files (.rm), PDF/PNG rendering
    ports/             repository and object store interfaces
    repository/mongo/  MongoDB connection, repositories, collection/index setup
    repository/memory/ in-memory repositories for tests
    service/           sign-in and users, device auth, uploads + spool, browser uploads,
                       Pocket, reMarkable, archive, transcription/reading and summary stages,
                       recording actions
    storage/s3/        S3 object store (storage/memory for tests)
    transport/http/    router, middleware, handlers
    web/               embedded frontend (dist/) + SPA handler
    worker/            background pipeline: claim, run stages, retry/backoff
desktop/               Electron desktop app (main process, server setup page, packaging config)
docs/                  device protocol, architecture, operations
frontend/
  public/              app icons (favicon.svg, PWA and Apple touch icons)
  vite.config.ts       build, dev proxy, PWA manifest and service worker (vite-plugin-pwa)
frontend/src/
  api/client.ts        API client
  auth.tsx             sign-in state (AuthProvider, useAuth)
  components/          reusable UI components
  pages/               NotesLayout (list sidebar + open note), Conversation (a note), Devices,
                       Users, Settings (tabs), Status, Account, Login
  components/NotesList the notes list (sidebar on desktop, start page on phones)
  context/             NotesContext: the notes list shared by sidebar and open note
  hooks/useAutosave.ts saving the open note's summary
  i18n/                translations (en.ts, de.ts) and language setup
  lib/                 display helpers (recordings, themes, error texts)
```

## Tests

```bash
make test
# MongoDB repository tests run when a database is available:
KNOWPOD_TEST_MONGO_URI=mongodb://localhost:27017 make test
```
