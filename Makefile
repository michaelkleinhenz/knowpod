# knowpod-service build. The frontend is built separately and embedded into the backend binary so a
# single artifact serves both the web UI and the API.

.PHONY: build frontend backend test clean version set-version desktop-deps desktop-run desktop desktop-linux desktop-windows desktop-mac desktop-all

# Server the desktop app connects to on its first start, e.g.
# `make desktop SERVER_URL=https://knowpod.example.com`. Without it the app asks for one.
SERVER_URL ?=
DESKTOP_FLAGS = --publish never -c.extraMetadata.version=$(VERSION) $(if $(SERVER_URL),-c.extraMetadata.defaultServerUrl=$(SERVER_URL))

# The app version, shown in the app's settings and used for the builds and release names. It is
# kept in the VERSION file; change it there or with `make set-version V=1.2.0`.
VERSION := $(shell cat VERSION)

# Print the app version.
version:
	@echo $(VERSION)

# Set the app version: writes VERSION and keeps the package.json files in step with it.
set-version:
	@test -n "$(V)" || { echo 'usage: make set-version V=1.2.0'; exit 1; }
	echo "$(V)" > VERSION
	cd frontend && npm version "$(V)" --no-git-tag-version --allow-same-version
	cd desktop && npm version "$(V)" --no-git-tag-version --allow-same-version

# Build the single self-contained binary (frontend embedded in the backend).
build: backend

# Build the SPA and stage it into the backend's embed directory.
frontend:
	cd frontend && npm ci && npm run build
	rm -rf backend/internal/web/dist
	cp -r frontend/dist backend/internal/web/dist

# Compile the backend with the freshly built frontend embedded.
backend: frontend
	cd backend && CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(VERSION)" -o bin/server ./cmd/server

# Desktop app (Electron, in desktop/): a native window around the web UI of a knowpod server.
# Installers land in desktop/dist. Build each platform on its own OS (macOS for desktop-mac,
# Windows or Linux with Wine for desktop-windows); desktop-all builds all three where the host
# can (a Mac, with Wine installed).
desktop-deps:
	cd desktop && npm ci

# Start the desktop app from source (SERVER_URL overrides the saved server for this run).
desktop-run: desktop-deps
	cd desktop && KNOWPOD_SERVER_URL=$(SERVER_URL) npx electron .

# Installers for the current OS.
desktop: desktop-deps
	cd desktop && npx electron-builder $(DESKTOP_FLAGS)

# Linux: AppImage, .deb and .tar.gz.
desktop-linux: desktop-deps
	cd desktop && npx electron-builder --linux $(DESKTOP_FLAGS)

# Windows: NSIS installer and .zip.
desktop-windows: desktop-deps
	cd desktop && npx electron-builder --win $(DESKTOP_FLAGS)

# macOS: .dmg and .zip.
desktop-mac: desktop-deps
	cd desktop && npx electron-builder --mac $(DESKTOP_FLAGS)

desktop-all: desktop-deps
	cd desktop && npx electron-builder --linux --win --mac $(DESKTOP_FLAGS)

test:
	cd backend && go test ./...

clean:
	rm -rf backend/bin frontend/dist desktop/dist
	find backend/internal/web/dist -mindepth 1 ! -name index.html -delete
	git checkout -- backend/internal/web/dist/index.html 2>/dev/null || true
