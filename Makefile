# knowpod-service build. The frontend is built separately and embedded into the backend binary so a
# single artifact serves both the web UI and the API.

.PHONY: build frontend backend test clean version set-version desktop-deps desktop-run desktop desktop-linux desktop-windows desktop-mac desktop-all android-deps android android-test ios-deps ios

# Server the desktop and mobile apps connect to on their first start, e.g.
# `make desktop SERVER_URL=https://knowpod.example.com`. Without it the app asks for one.
SERVER_URL ?=
DESKTOP_FLAGS = --publish never -c.extraMetadata.version=$(VERSION) $(if $(SERVER_URL),-c.extraMetadata.defaultServerUrl=$(SERVER_URL))
ANDROID_FLAGS = -PappVersion=$(VERSION) $(if $(SERVER_URL),-PdefaultServerUrl=$(SERVER_URL))
IOS_FLAGS = MARKETING_VERSION=$(VERSION) CURRENT_PROJECT_VERSION=$(VERSION_CODE) $(if $(SERVER_URL),KNOWPOD_DEFAULT_SERVER_URL=$(SERVER_URL))

# The app version, shown in the app's settings and used for the builds and release names. It is
# kept in the VERSION file; change it there or with `make set-version V=1.2.0`.
VERSION := $(shell cat VERSION)
# The build number of the mobile apps, derived from VERSION: 1.2.3 → 10203.
VERSION_CODE := $(shell echo $(VERSION) | awk -F'[.-]' '{ print $$1 * 10000 + $$2 * 100 + $$3 }')

# Print the app version.
version:
	@echo $(VERSION)

# Set the app version: writes VERSION and keeps the package.json files, the API spec and the
# Chrome extension's manifest and the iOS project in step with it (the Android app reads VERSION
# itself).
set-version:
	@test -n "$(V)" || { echo 'usage: make set-version V=1.2.0'; exit 1; }
	echo "$(V)" > VERSION
	cd frontend && npm version "$(V)" --no-git-tag-version --allow-same-version
	cd desktop && npm version "$(V)" --no-git-tag-version --allow-same-version
	cd mobile && npm version "$(V)" --no-git-tag-version --allow-same-version
	sed -i.bak -E 's/^(  version: ).*/\1$(V)/' backend/api/openapi.yaml && rm backend/api/openapi.yaml.bak
	sed -i.bak -E 's/^(  "version": ")[^"]*(",)/\1$(V)\2/' chrome-extension/src/manifest.json && rm chrome-extension/src/manifest.json.bak
	sed -i.bak -E 's/(MARKETING_VERSION = )[^;]*;/\1$(V);/' mobile/ios/App/App.xcodeproj/project.pbxproj && rm mobile/ios/App/App.xcodeproj/project.pbxproj.bak

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

# Android app (Capacitor, in mobile/): a WebView around the web UI of a knowpod server, plus
# copying from the Pocket over its WiFi. Needs Node.js 22, JDK 21 and the Android SDK
# (ANDROID_HOME, or sdk.dir in mobile/android/local.properties). The APK lands in mobile/dist;
# it is signed with the keystore in KNOWPOD_KEYSTORE (see README.md#android-app), else with the
# debug key.
android-deps:
	cd mobile && npm ci && npx cap sync android

android: android-deps
	cd mobile/android && ./gradlew assembleRelease $(ANDROID_FLAGS)
	mkdir -p mobile/dist
	cp mobile/android/app/build/outputs/apk/release/app-release.apk mobile/dist/knowpod-$(VERSION)-android.apk

# The Android app's unit tests: the Pocket protocol and WiFi transfer against a fake recorder.
android-test: android-deps
	cd mobile/android && ./gradlew testReleaseUnitTest $(ANDROID_FLAGS)

# iOS app (Capacitor, in mobile/ios/): the same WebView shell for iPhone and iPad, without the
# Pocket copy. Needs macOS with Xcode 26 and Node.js 22; Xcode opens
# mobile/ios/App/App.xcodeproj after `make ios-deps`. `make ios` builds an unsigned
# mobile/dist/knowpod-<version>-ios-unsigned.ipa, to be signed for a device (see
# README.md#ios-app).
ios-deps:
	cd mobile && npm ci && npx cap sync ios

ios: ios-deps
	cd mobile/ios/App && xcodebuild -project App.xcodeproj -scheme App -configuration Release -destination generic/platform=iOS -derivedDataPath build CODE_SIGNING_ALLOWED=NO $(IOS_FLAGS) build
	rm -rf mobile/dist/Payload && mkdir -p mobile/dist/Payload
	cp -R mobile/ios/App/build/Build/Products/Release-iphoneos/App.app mobile/dist/Payload/
	cd mobile/dist && rm -f knowpod-$(VERSION)-ios-unsigned.ipa && zip -qr knowpod-$(VERSION)-ios-unsigned.ipa Payload && rm -rf Payload

test:
	cd backend && go test ./...

clean:
	rm -rf backend/bin frontend/dist desktop/dist mobile/dist mobile/android/app/build mobile/ios/App/build
	find backend/internal/web/dist -mindepth 1 ! -name index.html -delete
	git checkout -- backend/internal/web/dist/index.html 2>/dev/null || true
