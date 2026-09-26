# Device upload protocol

This guide is for implementing the upload client on the recorder gadget. The formal API
definition is in [`backend/api/openapi.yaml`](../backend/api/openapi.yaml).

## Overview

The gadget uploads each finished recording as a WAV file in three steps:

1. **Create** an upload: `POST /api/v1/uploads` with the recording's ID, size and SHA-256.
2. **Send** the bytes: one or more `PATCH /api/v1/uploads/{uploadId}` requests.
3. **Done** when a response reports `"status": "received"`. The service then transcodes
   and archives the file on its own; the gadget may delete its local copy.

Every step can be retried safely. After any network error, ask the service where the
upload stands (`GET /api/v1/uploads/{uploadId}`) and continue from there.

## Authentication

Each gadget has its own token, created on the web UI's **Devices** page (see
[Operations](operations.md#provisioning-devices)). It looks like `kpd_` followed by 43
URL-safe characters. Send it on every request:

```
Authorization: Bearer kpd_…
```

A `401` means the token is wrong or has been revoked. Retrying won't help; the gadget needs a
new token.

Always use HTTPS in production. The token is a bearer credential.

## Before uploading

The whole file must be known before the upload is created, because the service needs its
total size and checksum up front. Upload a recording only after it has been finished and
closed.

For each recording the gadget needs:

| Field | Rules |
|---|---|
| `recordingId` | The gadget's own name for the recording, unique **per device** and stable across retries and reboots. 1–128 characters of `A–Z a–z 0–9 . _ -`. A timestamp plus a counter works well, e.g. `20260926T101500Z-0042`. |
| `size` | Total file size in bytes, at least 44. Default server maximum is 4 GiB. |
| `sha256` | SHA-256 of the complete file, 64 hex characters. |
| `recordedAt` | Optional. Start of the recording, RFC 3339 (`2026-09-26T10:15:00Z`). |
| `highlights` | Optional. Moments the user marked while recording (see [Highlights](#highlights)). |

Supported audio: integer PCM WAV with 8, 16 or 24 bits per sample, 1–8 channels and any
common sample rate. Float WAV and compressed formats are rejected. A header whose data
length is 0 or `0xFFFFFFFF` (written before the length was known) is accepted; the data is
then taken to run to the end of the file.

## Step 1: create the upload

```http
POST /api/v1/uploads
Authorization: Bearer kpd_…
Content-Type: application/json

{"recordingId": "20260926T101500Z-0042", "size": 9600044,
 "sha256": "2e8c25cc…", "recordedAt": "2026-09-26T10:15:00Z"}
```

Response (`201 Created` for a new upload, `200 OK` if this `recordingId` was created
before):

```json
{"uploadId": "35e83419efdebba9f2e42ec8", "recordingId": "20260926T101500Z-0042",
 "status": "uploading", "size": 9600044, "offset": 0}
```

Store `uploadId` with the recording. If the gadget loses it (e.g. after a reboot), repeat the
create call with the same values to get the same upload back, including its current
`offset`.

## Step 2: send the bytes

Send the file from `offset` onwards. The `Upload-Offset` header must equal the number of
bytes the service already has:

```http
PATCH /api/v1/uploads/35e83419efdebba9f2e42ec8
Authorization: Bearer kpd_…
Upload-Offset: 0
Content-Type: application/offset+octet-stream
Content-Length: 4194304

<bytes 0 … 4194303>
```

Response `200 OK`, with the new offset in the body and in the `Upload-Offset` header:

```json
{"uploadId": "35e83419efdebba9f2e42ec8", "recordingId": "20260926T101500Z-0042",
 "status": "uploading", "size": 9600044, "offset": 4194304}
```

Repeat with the next chunk until the response has `"status": "received"`.

**Chunk size** is up to the gadget. The whole file can go in a single PATCH. On unreliable
links, chunks of 1–8 MiB limit how much has to be resent after a failure. The service keeps
every byte it received, even from a request that broke off, so large chunks only cost the
time it takes to find out the connection failed.

The service streams the body to disk. Send `Content-Length` when possible: a chunk that
would go past the declared `size` is then rejected (`413`) before any of it is read.

## After an error

If a request fails with a network error or a timeout, ask for the current state and resume
from its `offset`:

```http
GET /api/v1/uploads/35e83419efdebba9f2e42ec8
Authorization: Bearer kpd_…
```

| Response | Meaning | What the gadget should do |
|---|---|---|
| `200`, `status: uploading` | In progress | PATCH from `offset`. |
| `200`, `status: received` or `stored` | Complete | Done. The local copy may be deleted. |
| `200`, `status: failed` | Rejected; `error` says why | Stop retrying this recording. Keep it for inspection if space allows. |
| `400` | Malformed request (the `error` field says which field) | Fix the request; don't retry unchanged. |
| `401` | Token invalid or revoked | Stop; the gadget needs a new token. |
| `404` on GET/PATCH | Upload unknown, e.g. purged after being idle too long (48 h by default) | Start over with step 1. The same `recordingId` gets a new upload. |
| `409` on create | This `recordingId` already exists with a different size or checksum | Bug on the gadget: a recording ID was reused. Pick a new ID. |
| `409` on PATCH | `Upload-Offset` doesn't match; the body's `offset` field and the `Upload-Offset` header give the right value | Resume from that offset. |
| `413` | File larger than the server maximum, or chunk overruns `size` | Don't retry unchanged. |
| `422`, checksum error | All bytes arrived but the SHA-256 doesn't match. The service discarded them and reset the offset to 0. | Resend the whole file from offset 0. If it happens repeatedly, the checksum the gadget computes is wrong. |
| `422`, unsupported WAV | The file arrived intact but isn't a supported WAV. The recording is now `failed`. | Stop retrying this recording. |
| `5xx` | Server-side problem | Retry with exponential backoff (e.g. 5 s, 10 s, 20 s … up to 10 min). |

A PATCH to an upload that is already complete returns `200` with its final state, so
retrying the last chunk after a lost response is harmless.

## Highlights

If the user presses the highlight button while recording, send the marked moments with the
recording. Each highlight is **either** the position in the recording or the wall-clock
time:

```json
"highlights": [
  {"offsetMs": 184000},
  {"at": "2026-09-26T10:18:04Z"}
]
```

- `offsetMs`: milliseconds from the start of the recording (preferred: it doesn't depend on
  the device clock).
- `at`: wall-clock time, RFC 3339. It needs `recordedAt` on the upload; the server converts
  it to an offset.

Put them in the create request (step 1). If the device only knows them later, or wants to
correct them, it can replace them at any time:

```http
PUT /api/v1/uploads/35e83419efdebba9f2e42ec8/highlights
Authorization: Bearer kpd_…
Content-Type: application/json

{"highlights": [{"offsetMs": 184000}, {"offsetMs": 912500}]}
```

The response lists the stored highlights, sorted and without duplicates. At most 1000
highlights per recording; offsets must lie within the first 24 hours. A repeated create
request with `highlights` also replaces them. The summary gets a "Highlights" section
describing what was said at each moment; highlights sent after the summary was written
appear in it after the next re-summarize.

## Reference client logic

```text
for each finished recording R (oldest first):
    if R.uploadId is unknown:
        up = POST /uploads {R.id, R.size, R.sha256, R.recordedAt}
        save R.uploadId = up.uploadId
    else:
        up = GET /uploads/R.uploadId          # on 404: forget uploadId, start over

    while up.status == "uploading":
        chunk = R.bytes[up.offset : up.offset + CHUNK]
        try:
            up = PATCH /uploads/R.uploadId, Upload-Offset: up.offset, body: chunk
        on 409 offset mismatch: up.offset = error.offset
        on 422 checksum:        up.offset = 0
        on network error/5xx:   wait with backoff; up = GET /uploads/R.uploadId

    if R.highlights changed after the create:
        PUT /uploads/R.uploadId/highlights {R.highlights}
    if up.status in ("received", "stored"): delete R locally
    if up.status == "failed":               mark R as rejected, move on
```

## Trying it by hand

```bash
API=https://knowpod.example.com/api/v1
TOKEN=kpd_…
SIZE=$(stat -c %s rec.wav); SHA=$(sha256sum rec.wav | cut -d' ' -f1)

ID=$(curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -d "{\"recordingId\":\"test-001\",\"size\":$SIZE,\"sha256\":\"$SHA\"}" $API/uploads | jq -r .uploadId)
curl -s -X PATCH -H "Authorization: Bearer $TOKEN" -H "Upload-Offset: 0" \
  --data-binary @rec.wav $API/uploads/$ID
```
