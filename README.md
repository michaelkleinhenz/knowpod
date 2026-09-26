# knowpod-service

Backend for the knowpod AI audio recorder. Recorder gadgets push WAV recordings to this
service, which verifies them, transcodes them losslessly to FLAC and archives them in S3.
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
```

- Each gadget authenticates with its own revocable token, created on the web UI's
  **Devices** page (or through the admin API).
- People sign in to the web UI with email and password. The first login uses
  `ADMIN_EMAIL`/`ADMIN_PASSWORD` from the environment; once the password is changed in the
  UI, the stored password replaces the one from the environment.
- Uploads are idempotent (the gadget names each recording), resumable after dropped
  connections, and checked against a SHA-256 the gadget declares up front.
- Transcoding and archiving run in a background worker with retries and backoff.
  Transcription and AI steps can be added as further worker stages.

Supported input: integer PCM WAV, 8/16/24 bit, 1–8 channels, up to 4 GiB.

## Documentation

| Document | For |
|---|---|
| [Device upload protocol](docs/device-protocol.md) | Implementing the upload client on the gadget: requests, error handling, retry logic |
| [Architecture](docs/architecture.md) | Backend developers: components, recording lifecycle, worker, data model, adding processing stages |
| [Operations](docs/operations.md) | Deploying and running: Railway, AWS/IAM setup, web UI sign-in, provisioning devices, monitoring, recovery, limitations |
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

Scripts use the admin API with `ADMIN_TOKEN` (set it in `.env`) instead of a browser
session.

```bash
set -a && . ./.env && set +a       # load ADMIN_TOKEN into the shell
API=http://localhost:8080/api/v1
ADMIN="Authorization: Bearer $ADMIN_TOKEN"

# Register a device; keep the token, it is shown only once.
TOKEN=$(curl -s -X POST -H "$ADMIN" -d '{"name":"test-recorder"}' $API/admin/devices | jq -r .token)

# Upload a WAV file.
SIZE=$(stat -c %s rec.wav); SHA=$(sha256sum rec.wav | cut -d' ' -f1)
ID=$(curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -d "{\"recordingId\":\"rec-001\",\"size\":$SIZE,\"sha256\":\"$SHA\"}" $API/uploads | jq -r .uploadId)
curl -s -X PATCH -H "Authorization: Bearer $TOKEN" -H "Upload-Offset: 0" \
  --data-binary @rec.wav $API/uploads/$ID

# Inspect it and fetch the archived FLAC.
curl -s -H "$ADMIN" $API/admin/recordings/$ID | jq
curl -s -H "$ADMIN" -o rec.flac $API/admin/recordings/$ID/audio
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
| `ADMIN_EMAIL` | _(empty)_ | Email of the default web UI login |
| `ADMIN_PASSWORD` | _(empty)_ | Password of the default login, until it is changed in the UI. Empty disables the default login. |
| `SESSION_TTL` | `168h` | How long a web UI sign-in lasts |
| `ADMIN_TOKEN` | _(empty: disabled)_ | Bearer token for scripting `/api/v1/admin/*` without signing in |
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

Objects are stored at `recordings/<deviceId>/<id>.flac` (and `.wav` when kept), where
`<id>` is the server-assigned recording ID. For the required IAM permissions, see
[Operations](docs/operations.md#s3).

## Layout

```
backend/
  api/openapi.yaml     API specification
  cmd/server/          entrypoint and wiring
  internal/
    audio/             WAV parsing, WAV → FLAC transcoding
    config/            environment-based configuration
    domain/            models: recording (lifecycle), device, user + session
    ports/             repository and object store interfaces
    repository/mongo/  MongoDB connection, repositories, collection/index setup
    repository/memory/ in-memory repositories for tests
    service/           web UI sign-in, device auth, resumable uploads + spool, archive stage
    storage/s3/        S3 object store (storage/memory for tests)
    transport/http/    router, middleware, handlers
    web/               embedded frontend (dist/) + SPA handler
    worker/            background pipeline: claim, run stages, retry/backoff
docs/                  device protocol, architecture, operations
frontend/src/
  api/client.ts        API client
  auth.tsx             sign-in state (AuthProvider, useAuth)
  components/          reusable UI components
  pages/               Login, Home, Devices, Status (health, API URLs + reference), Account
```

## Tests

```bash
make test
# MongoDB repository tests run when a database is available:
KNOWPOD_TEST_MONGO_URI=mongodb://localhost:27017 make test
```
