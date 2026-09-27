# Single-binary build for knowpod-service. The frontend SPA is built first, then its output is
# embedded into the Go backend so one binary serves both the API and the web UI.

# --- Stage 1: build the frontend SPA ---
FROM node:22-alpine AS frontend
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
# vite.config.ts reads the app version from ../VERSION.
COPY VERSION /VERSION
RUN npm run build

# --- Stage 2: build the Go backend with the frontend embedded ---
FROM golang:1.24-alpine AS backend
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Replace the placeholder web assets with the freshly built SPA, then compile. The embed
# directive in internal/web picks these up at build time.
RUN rm -rf ./internal/web/dist
COPY --from=frontend /app/dist ./internal/web/dist
COPY VERSION /VERSION
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=$(cat /VERSION)" -o /out/server ./cmd/server

# --- Stage 3: minimal runtime image ---
FROM alpine:3.20
RUN adduser -D -u 10001 app && apk add --no-cache ca-certificates \
    && mkdir -p /data/uploads && chown app:app /data/uploads
USER app
COPY --from=backend /out/server /server
# Spool for in-flight uploads and received WAV files awaiting archiving. Mount a persistent
# volume here (a Railway volume, or a named volume in docker compose) so partially uploaded
# recordings survive restarts. No VOLUME instruction: Railway rejects it.
ENV UPLOAD_DIR=/data/uploads
EXPOSE 8080
ENTRYPOINT ["/server"]
