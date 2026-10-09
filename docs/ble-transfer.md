# Bluetooth transfer from the recorder

The ESP32 recorder (`esp32/`) uploads its recordings to the backend over Wi-Fi with the
[device upload protocol](device-protocol.md). Where none of its Wi-Fi networks is in range, the
knowpod desktop or mobile app can take them over Bluetooth LE instead and upload them for it:

1. The recorder's upload fails for lack of Wi-Fi, so it advertises its transfer service (every
   100-200 ms, and it stays awake for up to 20 minutes). While an app is paired and the recorder
   has no Wi-Fi but nothing waits, it advertises too, slowly (every 1-1.5 s), so the app's "Copy"
   still connects and reports that nothing waits.
2. The app, paired with the recorder once, finds it, reads the recordings that wait for an upload
   and uploads each one to the backend with the device's own token, using the same device upload
   protocol as the recorder would.
3. When the backend has received a recording, the app tells the recorder, which marks the upload
   done exactly as if it had sent the file itself.

Because the upload is made with the device token and the recorder's own `recordingId`, the backend
files the recording under the device and deduplicates it like any device upload: a recording that
the app relayed and the recorder later tries again over Wi-Fi is found complete, never stored
twice. Interrupted transfers resume where they stopped, on both the Bluetooth and the backend side.

Wi-Fi stays the first choice. The recorder switches Bluetooth on only while uploads wait and Wi-Fi
can't be reached, and off again once nothing waits or Wi-Fi works. It can be switched off for good
in **Settings → Bluetooth transfer** on the device (`"bluetooth": false` in `config.json`).

## Requirements

- The recorder has a device token (`backend.token` in `config.json`, see
  [Operations](operations.md#provisioning-devices)); it hands it to the paired app for the upload.
- The recorder's backend (`backend.url`) is the server the app is connected to. The app refuses to
  relay to any other server.
- The app is paired with the recorder (below). Nothing else needs to be set up in the app; it looks
  for the recorder every two minutes while it runs, and **Copy now** looks right away.

## Pairing

Only paired apps may talk to the recorder. Pairing uses LE Secure Connections with a passkey
(MITM protection); every request needs the encrypted, authenticated link that results.

1. On the recorder: **Settings → Bluetooth transfer → Pair with app**. Pairing stays open for three
   minutes.
2. In the app: **Settings → Devices → Bluetooth recorder → Pair**, and pick the recorder
   (`knowpod-XXXX`, its name is shown on the device's pairing screen).
3. The recorder shows a six-digit code; enter it in the app (or in the system's pairing dialog on
   macOS, iOS and Android).

Outside pairing mode the recorder refuses new pairings and removes any bond made anyway.
**Forget paired apps** on the recorder removes all bonds.

## GATT service

| | UUID | Properties |
|---|---|---|
| Service | `6b6e7000-0b1e-4d0a-9c3e-6b6e6f77706f` | advertised; the scan response carries the name `knowpod-XXXX` |
| CONTROL | `6b6e7001-0b1e-4d0a-9c3e-6b6e6f77706f` | write (encrypted, authenticated), notify |
| DATA | `6b6e7002-0b1e-4d0a-9c3e-6b6e6f77706f` | notify |

The app subscribes to both characteristics, then writes one request at a time to CONTROL and waits
for its response. A request is a JSON object of at most 512 bytes. The response comes as
notifications of CONTROL, each `[flags: 1 byte][bytes]`; bit 0 of `flags` is set while more
fragments follow. The concatenated bytes are a JSON object with `ok` and the request's `op`; a
failure has `ok: false`, an `error` code (`bad-request`, `not-found`, `io`) and a `message`.

The recorder serves one app at a time and disconnects an app that asks nothing for five minutes.

### `info`

```json
{"op": "info"}
→ {"ok": true, "op": "info", "protocol": 1, "name": "knowpod-1A2B",
   "backend": "https://www.knowpod.de/api/v1", "token": "kpd_…", "pending": 2}
```

`backend` and `token` are empty while the recorder has no device token.

### `list`

The recordings that wait for an upload, oldest first (at most 50; ask again after relaying them):

```json
{"op": "list"}
→ {"ok": true, "op": "list", "recordings": [
     {"id": "20260926-101500", "size": 9600044, "duration": 300.0, "title": "Fri 26.09.2026 10:15",
      "recordedAt": "2026-09-26T08:15:00Z", "highlights": [184000]}]}
```

`highlights` are offsets in milliseconds, as the upload's `offsetMs`; `recordedAt` is missing when
the recorder's clock wasn't set.

### `open`

The size and SHA-256 of a recording's file, for creating the upload. Hashing a long recording takes
a while (allow two or three minutes):

```json
{"op": "open", "id": "20260926-101500"}
→ {"ok": true, "op": "open", "id": "20260926-101500", "size": 9600044, "sha256": "2e8c25cc…"}
```

### `read`

A range of the file (at most 256 KiB per request). The bytes come as DATA notifications, each
`[offset: uint32, little-endian][bytes]` of at most 244 bytes, so each fits one LE data packet
(Android lost most of the larger, fragmented ones), in order, followed by the response on CONTROL:

```json
{"op": "read", "id": "20260926-101500", "offset": 0, "length": 262144}
→ {"ok": true, "op": "read", "id": "20260926-101500", "offset": 0, "length": 262144}
```

`length` in the response is how much was sent; it may be less than asked for at the end of the
file or when the recorder couldn't read further. The app checks that the offsets are contiguous
and asks again from the first byte it is missing.

### `done`

The backend has the whole recording (`status` is `received`, `stored`, `transcribed` or
`summarized`):

```json
{"op": "done", "id": "20260926-101500", "uploadId": "35e83419efdebba9f2e42ec8", "status": "received"}
→ {"ok": true, "op": "done", "id": "20260926-101500"}
```

The recorder then marks the upload done and stops offering the recording.

## The app's side

The relay is the same in every app: `desktop/src/recorder-relay.js` (Electron, Web Bluetooth),
`mobile/android/…/recorder/` (Android) and `mobile/ios/App/App/Recorder/` (iOS). For each listed
recording it calls `open`, creates the upload (`POST /uploads` with the device token, `size`,
`sha256`, `recordedAt` and `highlights`), reads the file from the upload's `offset` in chunks of
1 MiB and sends each with `PATCH /uploads/{uploadId}`, following the error handling of the
[device upload protocol](device-protocol.md#after-an-error), and finally calls `done`. A `401`
stops the relay (the device token is wrong); temporary failures leave the recording for the next
attempt.
