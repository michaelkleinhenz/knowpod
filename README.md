# knowpod-service

A Go backend with an embedded React web UI, built and shipped as a single binary.

## Tech stack

| Layer | Technology |
|---|---|
| Backend | Go 1.24, chi router |
| Frontend | React 18 + TypeScript, Vite, react-router |
| Deploy | One binary (frontend embedded in the backend), Docker / docker-compose |

## One binary: frontend embedded in the backend

The frontend is built first (`vite build`) and its output is embedded into the backend
binary via Go `embed` (`backend/internal/web`). The binary serves both the UI and the API
from the same process and origin. That is why the `Dockerfile` lives in the project root
and produces a single image in a multi-stage build.

## Quick start (Docker)

```bash
docker compose up --build
```

- App (UI + API): <http://localhost:8080> — API under `/api/v1`, health check at `/healthz`

## Build the binary without Docker

```bash
make build
./backend/bin/server
```

## Local development

The Vite dev server gives hot reload and proxies `/api` to the backend, so no re-embedding
is needed.

```bash
cd backend && go run ./cmd/server     # :8080, serves the placeholder UI
cd frontend && npm install && npm run dev   # http://localhost:5173, proxies /api to :8080
```

## Configuration

Environment variables only:

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `FRONTEND_URL` | `http://localhost:5173` | Allowed CORS origin |

## Layout

```
backend/
  api/openapi.yaml     API specification
  cmd/server/          entrypoint
  internal/
    config/            environment-based configuration
    transport/http/    router, middleware, handlers
    web/               embedded frontend (dist/) + SPA handler
frontend/src/
  api/client.ts        API client
  components/          reusable UI components
  pages/               page components
```

## Tests

```bash
make test
```
