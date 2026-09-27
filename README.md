# knowpod-service

Backend for the knowpod AI audio recorder. Recorder gadgets push WAV recordings to this
service, which verifies them, transcodes them losslessly to FLAC, archives them in S3, and
then transcribes and summarizes them with AI models through OpenRouter.
Metadata lives in MongoDB. A Go backend with an embedded React web UI (password sign-in),
built and shipped as a single binary.

## Tech stack

| Layer | Technology |
|---|---|
| Backend | Go 1.24, chi router, official MongoDB driver, AWS SDK v2 |
| Database | MongoDB 7 as a single-node replica set (for multi-document transactions) |
| Audio storage | Amazon S3 (an existing bucket) |
| Transcoding | Pure-Go FLAC encoder ([mewkiz/flac](https://github.com/mewkiz/flac)), no external binaries |
| Frontend | React 18 + TypeScript, Vite, react-router |
| Deploy | One binary (frontend embedded in the backend), Docker / docker-compose |

## How recordings flow

```
gadget ──POST /uploads──▶ upload created (status: uploading)
       ──PATCH chunks──▶ streamed to the local spool (UPLOAD_DIR), resumable
                         last byte: SHA-256 + WAV header verified (status: received)
background worker ─────▶ WAV → FLAC, uploaded to S3 (status: stored), spool cleaned up
AI worker ─────────────▶ transcript (status: transcribed) → title + summary (status: summarized)
```

- **Users** sign in with email and password and each see only their own notes and
  devices. Admins manage users and the AI settings. The built-in admin is `ADMIN_EMAIL`,
  whose password is `ADMIN_PASSWORD` until one is set in the UI.
- Each gadget authenticates with its own revocable token, created on the web UI's
  **Devices** page.
- Each user can connect their own [Pocket](https://heypocket.com) recorder on the
  **Account** page: recordings arrive through the user's personal webhook and their audio is
  downloaded with the user's Pocket API key.
- WAV and MP3 files can be uploaded from the browser on **Notes**.
- Each user can pair their **reMarkable** cloud account on the **Account** page with a
  one-time code. All its documents (except the trash) are imported into the knowpod
  folder **reMarkable** (read only, never changed on the tablet): handwritten notebooks are rendered to PDF, PDFs and EPUBs are kept as they
  are, and a vision model reads the pages into text that is summarized like a transcript.
- Notes come in types: **audio** notes (recordings, transcribed and summarized),
  **text** notes, plain Markdown documents written in the browser (**+** on **Notes**), and
  **documents** from the reMarkable. The
  notes list shows each note's type as an icon; text notes are edited, copied, downloaded
  and deleted like summaries.
- Notes can carry **labels**: colored chips in the note's header, with your own labels
  defined on the spot or under **Settings → Labels**. The built-in **Task** label adds a
  check box to the note's icon in the list; the check mark is saved.
- The notes list shows notes **by time** (grouped by day) or in **folders**, like files.
  Folders can be nested, renamed and deleted (their notes move up, nothing is lost); drag
  notes and folders onto a folder to move them, or use the note's **Move to folder** button.
- Recorders can send **highlights** (moments marked with a button while recording); they
  are shown on the note's timeline and described in the summary.
- Summaries and transcripts can be downloaded as Markdown and text files, in the web app and
  through the API.
- Uploads are idempotent (the gadget names each recording), resumable after dropped
  connections, and checked against a SHA-256 the gadget declares up front.
- Transcoding and archiving run in a background worker with retries and backoff.
- Every archived recording is transcribed and summarized through
  [OpenRouter](https://openrouter.ai). The API key and both models are chosen by an admin
  on the web UI's **Settings** page.
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

Supported input: integer PCM WAV, 8/16/24 bit, 1–8 channels, up to 4 GiB.

## Documentation

| Document | For |
|---|---|
| [Device upload protocol](docs/device-protocol.md) | Implementing the upload client on the gadget: requests, error handling, retry logic |
| [Architecture](docs/architecture.md) | Backend developers: components, recording lifecycle, worker, data model, adding processing stages |
| [Operations](docs/operations.md) | Deploying and running: Railway, AWS/IAM setup, users and sign-in, Pocket, reMarkable, uploads, installing the app, AI settings, devices, monitoring, recovery, limitations |
| [OpenAPI spec](backend/api/openapi.yaml) | The formal API definition. The service serves it at `/api/v1/openapi.yaml` and `/api/v1/openapi.json`, and the web UI's **Status** page renders it as an API reference. |

## Quick start (Docker)

You need an existing S3 bucket and AWS credentials that can use it (see
[Operations](docs/operations.md#s3)).

```bash
cp .env.example .env      # fill in ADMIN_PASSWORD, the bucket, region and AWS credentials
docker compose up --build
```

- Web UI: <http://localhost:8080>. Sign in with `ADMIN_EMAIL` / `ADMIN_PASSWORD`, then
  change the password under **Account**.
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
