# KnowPod

KnowPod is a universal note taking and todo management app that brings everything you
capture into one place, and the service behind it. It integrates with your productivity
tools and gadgets: conversations recorded on AI audio recorders arrive transcribed and
summarized, handwritten reMarkable notebooks arrive as searchable text, and everything else
you write or plan lives next to them as notes, tasks and boards. Use it in the browser, as
an installed app on your phone, or as a desktop app on Windows, macOS and Linux.

A Go backend with an embedded React web UI, built and shipped as a single binary. Metadata
lives in MongoDB, files in S3, and AI models are reached through OpenRouter.

## What it does

- **Notes of every kind.** Markdown text notes, transcribed and summarized recordings,
  imported documents, photos and kanban boards (paste or drop pictures right into a note's text), organized in folders, sub-notes and labels,
  linked to each other by number, searchable, and available offline. Notes hold tables,
  start from templates, keep their earlier versions, can be printed (Ctrl+P prints just the note) and can be published on the web.
- **Write with AI.** Select text and have the AI improve, shorten, translate or reshape it
  (a table, a checklist of action items), or ask it to write or continue at the cursor.
- **Ask your notes.** Questions in your own words ("What did Anna say about the Q3
  budget?") are answered by AI from your notes, transcripts and documents, citing the notes
  and the moment in a recording.
- **Todo management.** Any note can be a task with a due date, time, repeat rule, priority
  and reminder, typed in plain English or German ("Call Anna tomorrow 3pm p1"). Action items
  from your conversations become tasks in one click, and reminders arrive as push
  notifications on your phone and computer. A daily briefing on the home page and a weekly
  review sum up what is due and what came in.
- **Integrations with your tools and gadgets.**

  | Source | What arrives in knowpod |
  |---|---|
  | knowpod recorders and other gadgets | Recordings over a resumable [upload API](docs/device-protocol.md), transcribed and summarized, with highlights |
  | [Pocket](https://heypocket.com) recorders | Recordings through a personal webhook |
  | [reMarkable](https://remarkable.com) tablets | Notebooks, PDFs and EPUBs, with handwriting read into text |
  | The app itself | Voice memos recorded in the browser, with highlights |
  | Audio files, photos and PDFs | Uploads from the browser; photos of whiteboards and pages are read into text |
  | Other apps on your phone | Links, text, photos, PDFs and audio shared with the installed app |
  | AI models ([OpenRouter](https://openrouter.ai)) | Transcripts, titles, summaries and action items |
  | Scripts and your own tools | The full [REST API](backend/api/openapi.yaml), downloads as Markdown and text |
  | AI assistants (Claude, ChatGPT) | Your notes and tasks through an MCP server |

- **Everywhere you work.** A responsive web app, installable on phones and desktops (PWA),
  a native [desktop app](#desktop-app), an [Android app](#android-app) and an
  [iOS app](#ios-app); English and
  German, light and dark.

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
| Android app | Capacitor 8 (WebView shell, native Java for the Pocket), optional |
| iOS app | Capacitor 8 (WKWebView shell, Swift), optional |

## Features

- **Users** sign in with email and password and each see only their own notes and
  devices, plus the notes others shared with them. Admins manage users and the AI settings, and can download a full backup (database and S3 files) and restore from one, in the UI or with `ADMIN_TOKEN` ([details](docs/operations.md#built-in-backup-and-restore)). Every user can download a backup of their own notes and content and restore it (Settings → Backup, [details](docs/operations.md#personal-backup-and-restore)).
- **Sharing.** Any note (a todo list, say) can be shared with other users, together with
  everything under it, for viewing or editing. Changes show up for everyone right away;
  each person files a shared note in their own folder, with their own labels and reminders.
  Whole folders can be shared the same way, with all their notes and folders, including
  what is added later.
  Edits made at the same time never undo each other: a conflicting text edit asks which
  version stays. The built-in admin is `ADMIN_EMAIL`,
  whose password is `ADMIN_PASSWORD` until one is set in the UI.
- Each gadget authenticates with its own revocable token, created on the web UI's
  **Settings → Devices** tab.
- Each user can connect their own [Pocket](https://heypocket.com) recorder on the
  **Settings → Account** tab: recordings arrive through the user's personal webhook and their audio is
  downloaded with the user's Pocket API key.
- WAV and MP3 files, photos and PDFs can be uploaded from the browser on **Workspace**.
  Markdown files (`.md`) become text notes, titled by their first heading (or front matter
  `title`, or file name).
- Each user can pair their **reMarkable** cloud account under **Settings → Account** with a
  one-time code. All its documents (except the trash and names you choose to ignore) are imported into the knowpod
  folder **reMarkable**, in the same folders as on the tablet (read only, never changed on the tablet): handwritten notebooks are rendered to PDF, PDFs and EPUBs are kept as they
  are, and a vision model reads the pages (and any typed text) into text that is summarized like a transcript. These notes are read-only in knowpod too (their title and text can't be edited, as edits can't go back to the tablet); labels, tasks and folders still work.
  The other way, **text notes** put into the reMarkable folder (or a folder inside it) are sent to the tablet as
  e-books (EPUB) into the same folder there, and follow the note's edits; they stay editable in knowpod and show the
  reMarkable logo with a pen. They are sent at a smaller text size than the tablet's default (change it on the tablet and it stays). What you write or draw on such an e-book on the tablet is kept with the note as a PDF attachment, **reMarkable scribbles.pdf**, on blank pages (not the note's text), updated with the next import. A note that leaves the folder or goes into the trash has its e-book put into the tablet's
  trash. The reMarkable folder can't be deleted while a reMarkable is paired.
- Notes come in types: **audio** notes (recordings, transcribed and summarized),
  **text** notes, plain Markdown documents written in the browser (**+** on **Workspace**), and
  **documents** from the reMarkable. The
  workspace list shows each note's type as an icon; text notes are edited, copied, downloaded
  and deleted like summaries.
- Every note has a **number** of its own (#1, #2, … per user, never reused). Typing **#** in
  a note's text opens a list of your notes, filtered by number (or title) as you type; the
  chosen note is linked as "#12" (Ctrl/⌘+click opens it). Search also finds notes by number.
- **Due marks** mark a paragraph or list item as due without making a task note of it: type a
  date in brackets after it, such as **[today]**, **[fri]**, **[next monday]**, **[5.10.]** or
  **[tomorrow 3pm]** (German works, too: **[heute]**, **[morgen]**). As the "]" is typed it
  becomes the date and the language it was typed in ("[2026-10-01 en]"; Backspace right after
  undoes that), so it stays right on the following days. The mark is shown relative to today in
  that language, such as "[tomorrow]", "[morgen]", "[Friday]" or "[vor 3 Tagen]" (put the cursor
  in it to see and edit the date), and its whole line is colored by when it is due: red overdue,
  orange today, green within a week, the accent color later; checked-off items aren't colored.
- **Mentions**: typing **@** in a note's text opens a list with **Date** and the people you
  share notes with, filtered as you type. A person is shown as a pill ("@anna.berg", kept in the
  Markdown as "@anna.berg@example.com"). **Date** opens a calendar (arrow keys move the day,
  Page Up/Down the month, Enter takes it); the date is shown as a pill ("Mon, Oct 5, 2026",
  kept as "@2026-10-05"), and clicking it (or Enter on it) opens the calendar again to change it.
- **Table columns** are resized by dragging the border between them (with a mouse). The widths
  are kept in a comment above the table ("<!-- colwidths: 200 0 120 -->", 0 for a column of
  natural width), which other Markdown readers don't show.
- **Boards** are kanban boards, listed like any other note: each shows the notes of a folder,
  with a label or found by a saved filter as cards, all starting in the first column. New boards have the columns Todo,
  In Progress and Done; columns can be renamed, added and deleted, and cards are dragged
  between them.
- A note's page shows its title, then one compact row with its number, date, labels and
  icon actions; a dot in the top right corner shows whether its edits are saved. On wide
  screens a sidebar next to the note (not on boards) holds its icon actions, task (check
  box, date, priority), labels and details (date, type, duration, folder, boards, status,
  model), and the header keeps only the title; on narrower screens they stay in
  the header.
- Notes can carry **labels**: colored chips on the note's page, with your own labels
  defined on the spot or under **Settings → Labels**. The built-in **Task** label adds a
  check box to the note's icon in the list; the check mark is saved.
- **Tasks** have a due date, an optional time, a repeat rule ("every weekday", "every 2
  weeks", "every third Friday"), a reminder and a priority (P1–P3), set from the **Date** button on the note's
  page. Dates can be typed in English or German ("tomorrow 3pm", "jeden Montag", "am
  5.10."), also in a note's title. The **Tasks** view lists the open tasks by due date and
  adds new ones from one line ("Call Anna tomorrow 3pm p1"); checking off a recurring task
  moves it to its next date, and the double check mark in the note's sidebar completes it
  for good.
- Summaries list the **action items** found in the conversation; each becomes a task under
  the note with one click, due on the date that was named.
- **Tables**: type **/table** for a table with a header row; inside one, a toolbar adds and
  deletes rows and columns. Tables are stored as Markdown (GitHub style), so they show up in
  downloads, on the reMarkable and on published pages.
- **Templates**: **+ New Note** and **+ New Task** can start from a template: built-in ones
  (meeting notes, one-on-one, project brief, daily journal, weekly plan) or any text note
  marked **Use as template** in its sidebar. **/template** in a note inserts one at the
  cursor. `{{date}}`, `{{isoDate}}`, `{{time}}`, `{{weekday}}` and `{{title}}` are filled in.
  The filter word `template` lists your templates.
- **Version history**: the clock button on a note shows the earlier versions of its title
  and text and restores one. While you edit, a version is kept at most every 10 minutes;
  restoring and regenerating a summary always keep the text they replace, so they can be
  undone. The newest 50 versions of each note are kept.
- **Publish to the web**: the owner of a note publishes it from the **Share** panel. Anyone
  with the link (`/p/…`) reads its title and text, with its pictures, without signing in;
  sub-notes, attachments, labels and other notes stay private. **Stop publishing** takes the
  link down, and it stops working while the note is in the trash.
- **Writing with AI**: select text and click **Ask AI** (or press Ctrl/⌘+J, or type **/ai**
  on an empty line). Choose an action (improve, fix spelling and grammar, shorter, longer,
  simpler, more professional or casual, summarize, find action items, turn into a table,
  translate) or say what to do in your own words; at the cursor, the AI continues the text
  or writes what you ask for. The answer is shown first, to replace the selection with,
  insert below it, try again or discard. It uses the summary model.
- **Saved filters**: the search box understands a filter language like Todoist's, e.g.
  `label:Task & due:week & !done`, `@Work | folder:"Side projects"` or `(p1 | p2) overdue`
  (plain words still search the titles). In shared notes, `task & from:me` finds the tasks you
  made and `shared:bob` the notes bob shared with you (`owner:`, `assignee:`, `shared:me`,
  `mine`); notes from someone else show their initials in the list. A search can be saved as a filter and pinned below
  the search box, where one click narrows the list to it; **Settings → Filters** edits them
  and explains the language.
- **Time tracking**: tasks get an estimate ("45", "1h30"), and a timer on the note (or a
  25-minute focus session that stops by itself) logs the time spent on it. The running timer
  shows above the workspace list. **Time** lists the week's log (totals per note against their
  estimates, entries per day, time added by hand) and exports it as CSV.
- **Calendar feed**: **Settings → Account → Calendar** makes a private iCalendar link to
  subscribe to in Google Calendar, Apple Calendar or Outlook. It lists the open tasks with
  dates, repeating like the tasks, with their reminders as alarms.
- **AI assistants (MCP)**: knowpod's [MCP](https://modelcontextprotocol.io) server at `/mcp`
  lets AI assistants search, read, create and change your notes and tasks. Add the server URL
  to Claude (custom connector, or `claude mcp add` in Claude Code) or ChatGPT (developer mode
  app): they sign in through **OAuth**, and you allow them on knowpod's consent page. Only your
  own notes are reachable; **Settings → Account → AI assistants** lists the connected
  assistants to disconnect them, and makes a personal access token (bearer token) for tools
  without OAuth.
- **Ask**: the **Ask** page (or the ✦ button next to the search box) answers questions from
  your notes and those shared with you, with follow-up questions. There is no search index:
  the summary model reads a catalog of your notes (title, date, the start of the text) and
  picks the ones that fit the question, also by meaning and across English and German, and
  names words to look for, which are then found in the full texts and transcripts. It
  answers from those notes, citing each as [1], [2], … with a supporting quote; a quote from
  a recording links to the moment it is said. AI assistants get the same search through the
  MCP tool `find_notes`.
- **Speakers**: transcripts label speakers "Speaker 1", "Speaker 2", …; **Speakers** in the
  note's sidebar (above the transcript on a phone) gives them their names in the transcript
  and makes the summary again with them (or, unticked, replaces the labels in the summary and
  the action items). Names the AI recognized in the conversation ("Hi, I'm Anna") are offered
  with one click.
- **Briefings**: **Settings → Account → Briefings** turns on a daily briefing and a weekly
  review, made at the time you choose (in your time zone) as notes in the folder
  **Briefings** and announced by a notification. The briefing lists the tasks due today and
  overdue, the notes that came in since the day before with an AI digest, and open action
  items; the weekly review sums up the week before: new notes, tasks done, time logged, and
  what is overdue, coming up or waiting for over 30 days. Both can also be made right away.
  The daily briefing can also be sent by email (off by default; turn it on in the same place
  once an admin has set up email under **Admin → General → Email (Amazon SES)**, where a test
  email can be sent to check the setup).
- **Voice memos**: **Record** (the microphone next to the search box) records in the
  browser, also while you move around the app; mark moments as highlights while recording.
  The memo is saved as a WAV file and transcribed and summarized like any recording.
- **Photos and PDFs**: uploads (and the phone's camera, offered by the upload button) take
  photos (JPEG, PNG, WebP, GIF) and PDFs. The document model writes down their text (a
  photo without text is described), which is then summarized.
- **Share to knowpod**: installed as an app on a phone, knowpod is a share target: links and
  text shared with it become a note, as do Markdown files; photos, PDFs and audio files are
  uploaded.
- **Reminders** arrive as push notifications in every browser or installed app they are
  turned on in (**Settings → Account → Notifications**), on phones too (on iPhone: from the Home Screen
  app).
- The workspace list shows notes by **creation** (grouped by day), by **due** date, in
  **folders**, like files, or as open **tasks**.
  Folders can be nested, renamed and deleted (their notes move up, nothing is lost); drag
  notes and folders onto a folder to move them, or use the note's **Move to folder** button.
  Drop an item on the top or bottom edge of another to put it in order; the order is kept.
  New notes made in the folder view go into the folder opened last.
- Notes can hold **sub-notes**, like a folder whose head is a note itself: **New sub-note**
  below a note creates one, and dropping a note onto another note in the folder view moves
  it under that note. Sub-notes open and close under their parent, in the folder view and in
  the note's own sub-items list (Alt+click opens or closes a whole tree; opening a note
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
- The web UI's **Workspace** page (a note per recording) lists your recordings by the title of
  their summary; on desktop the list stays in a sidebar next to the open note, and notes open
  in tabs above it (Ctrl/Cmd+click, a middle click or **Open in new tab** in a note's context
  menu opens one in a new tab; drag tabs to reorder them, middle-click or × to close them);
  each note shows its summary, transcript and audio, and can be re-transcribed,
  re-summarized (the ✦ **Redo summary** button: summarizes the existing transcript again
  with a theme of your choice, the default Auto preselected) or deleted from icon buttons next to the view switcher. Summaries are always editable in place, like a document
  (type **/** for headings, lists and tasks; select text to format it), and save
  automatically (stored as Markdown). Under **Summary details** each summary's language, model and
  **theme** (its structure, e.g. meeting or call notes) can be changed and regenerated.
  Users adjust the built-in themes and add their own on the **Settings** page.
- The web UI is available in English and German; each user picks the language in
  **Settings**.
- The web UI has a light and a dark color scheme; each user picks one, or follows the
  device's setting, under **Settings → General**.
- The web UI works on phones and can be installed as an app (PWA).
- A **desktop app** for Windows, macOS and Linux is an alternative to the browser: the same
  web UI in a native window, signed in to and talking to the server exactly like the web app
  (see [Desktop app](#desktop-app)).
- An **Android app** does the same on phones, and copies recordings from a Pocket recorder
  over Bluetooth and the recorder's WiFi (see [Android app](#android-app)).
- An **iOS app** does the same on iPhone and iPad, including the Pocket copy (see
  [iOS app](#ios-app)).

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
it asks for the server's address (**File → Change Server…** changes it later; on Windows and Linux
**Alt** opens the menu). The window has no title bar: the app's header takes its place, with the
window's buttons drawn into it (see `desktop/src/titlebar.css`).

```bash
make desktop-run SERVER_URL=http://localhost:8080   # start it from source against a server
make desktop                                        # installers for this OS, in desktop/dist
make desktop-linux                                  # AppImage, .deb, .tar.gz
make desktop-windows                                # NSIS installer (needs Windows or Wine), .zip
make desktop-mac                                    # .dmg, .zip (needs macOS)
make desktop SERVER_URL=https://knowpod.example.com # preset the server, no question on first start
```

Requires Node.js 22. Build each platform on its own OS; the **Desktop and mobile apps** GitHub Actions
workflow (`.github/workflows/desktop.yml`, run by hand or on a `desktop-v*` tag) builds all
three and keeps the installers as artifacts; a `desktop-v<version>` tag also publishes them as
a GitHub release (see [Version](#version)). The builds are not code-signed, so macOS
Gatekeeper and Windows SmartScreen warn on first open. The desktop app shows notifications
while it runs; closing its window keeps it running in the tray, and it copies new
recordings from a Pocket recorder, plugged in by USB (switching the recorder's USB drive on
over Bluetooth first when needed) or over the recorder's own WiFi on Linux and Windows (see
[Operations](docs/operations.md#desktop-app)).

## Android app

`mobile/` holds a [Capacitor](https://capacitorjs.com) app for Android (`mobile/android/`): like
the desktop app, a WebView around the web UI of a knowpod server, with no backend or frontend
of its own. On the first start a bundled page (`mobile/www/`) asks for the server's address;
**Settings → General → Desktop and mobile apps → Change server** changes it later. Beyond the web
app it copies new recordings from a Pocket recorder over the recorder's WiFi, which a browser
can't (it needs Bluetooth, joining the recorder's network and a plain TCP connection): the
same **Pocket Sync** dialog as the desktop app, WiFi only. The Pocket protocol is a Java port
of the desktop app's (`mobile/android/app/src/main/java/net/kleinhenz/knowpod/pocket/`).

```bash
make android                                        # mobile/dist/knowpod-<version>-android.apk
make android SERVER_URL=https://knowpod.example.com # preset the server, no question on first start
make android-test                                   # unit tests: the Pocket protocol against a fake recorder
```

Requires Node.js 22, JDK 21 and the Android SDK (`ANDROID_HOME`, or `sdk.dir` in
`mobile/android/local.properties`); Android Studio opens `mobile/android/` after
`make android-deps`. The app needs Android 7 or newer, copying long Pocket recordings over WiFi Android 10 or
newer (older phones copy them over Bluetooth). The
**Desktop and mobile apps** workflow builds and tests the APK next to the desktop installers
and attaches it to the `desktop-v<version>` release.

**Signing.** Android installs an update only when it is signed with the same key as the
installed app. Create a keystore once and keep it safe:

```bash
keytool -genkeypair -v -keystore knowpod.jks -alias knowpod -keyalg RSA -keysize 4096 -validity 10000
```

For local builds, point `KNOWPOD_KEYSTORE` at it and set `KNOWPOD_KEYSTORE_PASSWORD`,
`KNOWPOD_KEY_ALIAS` and `KNOWPOD_KEY_PASSWORD`. For the workflow, add the repository secrets
`ANDROID_KEYSTORE_BASE64` (`base64 -w0 knowpod.jks`), `ANDROID_KEYSTORE_PASSWORD`,
`ANDROID_KEY_ALIAS` and `ANDROID_KEY_PASSWORD`. Without a keystore the APK is signed with a
debug key: it installs, but the next build can't update it (uninstall first).

## iOS app

`mobile/ios/` holds the iOS side of the same Capacitor app, for iPhone and iPad (iOS 15 or
newer): the web UI of a knowpod server in a WKWebView, with the same setup page
(`mobile/www/`) on the first start and **Settings → General → Desktop and mobile apps →
Change server** later. It shows the server's notifications while it is open, and opens a
clicked one's note. Like the Android app it copies new recordings from a Pocket recorder,
set up with the recorder's Bluetooth address and session key: short ones over Bluetooth,
long ones over the recorder's WiFi (a Swift port of the Android app's, in
`mobile/ios/App/App/Pocket/`; iOS hides Bluetooth addresses, so the app finds the Pocket
nearby and remembers it once the session key unlocked it). The native code is in Swift
(`mobile/ios/App/App/`).

```bash
make ios                                        # mobile/dist/knowpod-<version>-ios-unsigned.ipa
make ios SERVER_URL=https://knowpod.example.com # preset the server, no question on first start
```

Requires macOS with Xcode 26 and Node.js 22; Xcode opens `mobile/ios/App/App.xcodeproj`
after `make ios-deps` (the Capacitor library comes in as a Swift package, no CocoaPods). The
**Desktop and mobile apps** workflow builds the IPA next to the APK and the desktop
installers and attaches it to the `desktop-v<version>` release.

**Signing.** iOS installs only signed apps, and the IPA is unsigned. To put it on a device,
run it from Xcode with your Apple ID as the team (**Signing & Capabilities**), or re-sign the
IPA with your own certificate and provisioning profile (e.g. with Sideloadly, AltStore or
`codesign`); the bundle ID is `net.kleinhenz.knowpod`. Joining the Pocket's WiFi needs the
**Hotspot Configuration** capability (`mobile/ios/App/App/App.entitlements`), which the
unsigned IPA doesn't carry and a free Apple ID can't grant; sideloaded or signed without
it, the app copies all Pocket recordings over Bluetooth.

## Version

The app version lives in the [`VERSION`](VERSION) file at the repository root, a single line
such as `0.1.0`. Edit it there, or run `make set-version V=1.2.0`, which also keeps
`frontend/package.json`, `desktop/package.json`, `mobile/package.json`, the API spec (`backend/api/openapi.yaml`), the
Chrome extension's `manifest.json` and the iOS project in step. It is used by:

- the web app, which shows it under **Settings → General → About knowpod** (in the desktop
  app, next to the desktop app's own version);
- the server binary (`make build` and the Docker image), which logs it on start;
- the desktop installers, whose file names and app metadata carry it
  (`knowpod-<version>-<os>-<arch>.<ext>`);
- the Android app (`knowpod-<version>-android.apk`), whose version name it is; the version
  code is derived from it (1.2.3 → 10203);
- the iOS app (`knowpod-<version>-ios-unsigned.ipa`), whose version it is; the build number
  is derived from it the same way.

To release the desktop and mobile apps, bump `VERSION`, commit, and push a tag `desktop-v<version>`
(e.g. `git tag desktop-v1.2.0 && git push origin desktop-v1.2.0`). The workflow refuses a
tag that doesn't match `VERSION`.

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
mobile/                Capacitor apps (android/: native project and Pocket sync; ios/: Xcode project; www/: setup page)
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
  components/NotesList the workspace list (sidebar on desktop, start page on phones)
  context/             NotesContext: the workspace list shared by sidebar and open note
                       NoteTabs: the notes open in tabs on desktop
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
