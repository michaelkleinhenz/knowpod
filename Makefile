# knowpod-service build. The frontend is built separately and embedded into the backend binary so a
# single artifact serves both the web UI and the API.

.PHONY: build frontend backend test clean

# Build the single self-contained binary (frontend embedded in the backend).
build: backend

# Build the SPA and stage it into the backend's embed directory.
frontend:
	cd frontend && npm ci && npm run build
	rm -rf backend/internal/web/dist
	cp -r frontend/dist backend/internal/web/dist

# Compile the backend with the freshly built frontend embedded.
backend: frontend
	cd backend && CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/server ./cmd/server

test:
	cd backend && go test ./...

clean:
	rm -rf backend/bin frontend/dist
	find backend/internal/web/dist -mindepth 1 ! -name index.html -delete
	git checkout -- backend/internal/web/dist/index.html 2>/dev/null || true
