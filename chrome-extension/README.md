# knowpod Web Clipper

A Chrome extension (Manifest V3) that clips the page you are on into a knowpod note.

- Click the toolbar button, then **Clip this page**.
- The page's text (or your selection, if you have one) is extracted as Markdown: headings,
  paragraphs, lists, links, bold/italic, code and quotes. Menus, footers and other page
  furniture are left out.
- It is saved as a text note in your top-level **Clipped** folder (created when missing). The
  note starts with `Source: [title](url)` and the clip date, then the text.
- **No AI summary.** The text is stored as extracted; nothing is sent to any model by the extension.
  Long pages are cut at the 100,000 characters a note can hold, with a note saying so.

## Setup

1. `make package`, or load `src/` unpacked: `chrome://extensions` → Developer mode → Load unpacked.
2. Open the extension's **Options**, enter your knowpod server URL (e.g. `https://knowpod.example.com`)
   and allow access to it when Chrome asks.
3. Sign in to knowpod in the same browser. The extension uses that session (the
   `knowpod_session` cookie) and never sees or stores your password; if you are signed out it opens
   the server so you can sign in.

## Development

No build step and no dependencies (Node 20+ for the checks).

| Command | |
|---|---|
| `make check` | validate the manifest, check that referenced files exist, syntax-check the scripts |
| `make test` | `check` plus unit tests |
| `make package` | build `dist/knowpod-web-clipper-<version>.zip` |
| `make clean` | remove `dist/` |

API used: `GET/POST /api/v1/folders`, `POST /api/v1/recordings/text`
([openapi.yaml](../backend/api/openapi.yaml)).
