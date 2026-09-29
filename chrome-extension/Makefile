# knowpod Web Clipper (Chrome extension). No build step or dependencies: the extension is the
# src/ directory. `make package` zips it for the Chrome Web Store or sideloading.

.PHONY: help check test package clean version

SRC := src
DIST := dist
VERSION := $(shell node -p "require('./$(SRC)/manifest.json').version" 2>/dev/null)

help:
	@echo "make check    validate the manifest and syntax-check the scripts"
	@echo "make test     run the unit tests"
	@echo "make package  build $(DIST)/knowpod-web-clipper-<version>.zip"
	@echo "make clean    remove $(DIST)"
	@echo "Load unpacked: chrome://extensions -> Developer mode -> Load unpacked -> $(SRC)/"

# Manifest is valid JSON, every file it names exists, and every script parses.
check:
	@node scripts/check.mjs

test: check
	node --test test/*.test.mjs

package: check
	@mkdir -p $(DIST)
	rm -f $(DIST)/knowpod-web-clipper-$(VERSION).zip
	cd $(SRC) && zip -qr ../$(DIST)/knowpod-web-clipper-$(VERSION).zip . -x '.*'
	@echo "Built $(DIST)/knowpod-web-clipper-$(VERSION).zip"

version:
	@echo $(VERSION)

clean:
	rm -rf $(DIST)
