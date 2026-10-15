# AIREC recorder — Bluetooth protocol

A second recorder, by a different manufacturer from the Pocket. Worked out from one capture of
its Android app (`com.record.airec.google` 2.1.5) talking to the recorder (firmware **1.3.3**),
plus a read-only `airec-probe` run against the recorder itself (2026-10-15). See the capture tool
in [README.md](README.md) (`vendor-app` scenario) and the probe in `pocket_wifi_probe/airec.py`.

Everything here is confirmed against the hardware unless marked as a guess.

## Advertising and link

- The recorder advertises over **Bluetooth LE** as `AIREC(BLE)`, service `af30`, and also answers
  classic-Bluetooth inquiries (dual-mode). Everything below is LE.
- **No pairing, no unlock, no encryption.** Any BLE central in range can connect and list,
  download and delete recordings. The app's "pairing" only ties the recorder's serial number to
  the user's account on the vendor's cloud (`usa.jnnrec.com`); it does not protect the link. See
  [Security](#security).
- One connection at a time: while a phone is connected, another central can't connect.
- The phone asks for an ATT MTU of 517; the recorder grants 247. Notifications of file data were
  244 bytes.

## GATT

One service carries everything: `0011200a-2233-4455-6677-8899dfdedddc`.

| Characteristic                         | Properties        | Use |
|----------------------------------------|-------------------|-----|
| `0011201a-…`                           | notify            | live audio while the recorder is recording |
| `0011202a-…`                           | write no response | commands, phone → recorder |
| `0011203a-…`                           | notify            | command replies and unsolicited events |
| `0011204a-…`                           | notify            | file data during a download |
| `0011206a-…`                           | write no response | firmware update (by the app's naming; not seen in use) |

The short UUIDs `0011200a` / `0011201a` / … are the vendor's; they are **not** the knowpod
recorder's `6b6e70xx` UUIDs, and not the Pocket's `001120ax` UUIDs (close, but a different
vendor suffix `…dfdedddc`).

## Framing

- **Command** (write to `0011202a`): `55 AA <len> <cmd> <data…>`, where `len = 1 + len(data)`.
- **Reply / event** (notify on `0011203a`): `AA 55 <len> <cmd> <data…>`, same `len`.
- One notification may carry several frames back to back, or part of one; reassemble by the
  `AA 55 <len>` framing. In the probe run no reply bytes fell outside a frame.
- Multi-byte numbers (sizes, offsets) are **big-endian**.
- Recording names are their start time as 14 ASCII digits, `YYYYMMDDHHMMSS` (local time).

## Commands

Confirmed against the recorder unless noted. "→" is the reply frame(s).

| Cmd  | Request data            | Reply                                   | Meaning |
|------|-------------------------|-----------------------------------------|---------|
| `01` | —                       | `01` + serial (ASCII, e.g. `206112260800013`); then an unsolicited `36 00` | serial number |
| `20` | —                       | `20 10`                                 | chip type / audio format; app reads `0x10` as chip type 1, format 0 |
| `12` | —                       | `12` + version text (`1.3.3`)           | firmware version |
| `29` | —                       | `29` + text (`A5AB`)                     | model / hardware code |
| `26` | —                       | `26` + 13 bytes (`01 01 01 00 3c 00 00 00 1e 01 05 01 01`) | settings; fields not decoded |
| `0f` | —                       | `0f 02`                                 | unknown state byte |
| `0e` | —                       | `0e <b>`                                | battery percent (0–100) |
| `0b` | —                       | `0c <u32>` then `0d <u32>`              | storage: free, then total (MB, a guess: 29805 / 29818 ≈ 29 GB) |
| `30` | —                       | `30` + 14 ASCII digits                  | read the clock |
| `02` | 14 ASCII digits         | `02`                                    | set the clock; the app does this on every connect |
| `05` | —                       | one `05 <name><u32 size>` per recording, then `06` | list recordings |
| `07` | `<name><u32 offset>`    | `07 <name><u32 size>`, file bytes on `0011204a`, then `09` | download; offset resumes |
| `0a` | `<name>` (after `34 00 01`) | `0a` on success, `fc` on error       | delete a recording |
| `34` | `00 01`                 | (none)                                  | sent right before `0a`; see [open questions](#open-questions) |
| `22` | `00 3c`                 | `22`                                    | unknown (`0x3c` = 60) |
| `36`, `69 00`, `66 00` | —         | (none seen)                             | unknown; the app sends `69`/`66` before each download |

Confirmed by `airec-probe`:

- **No auth:** a laptop connected with no pairing and every command answered, exactly as the app
  saw. `01` returned the serial and the unsolicited `36 00` followed.
- **Download:** completed with the byte count equal to the announced size and ended with `09`,
  at ~44 KB/s. The app's `69 00` / `66 00` before a download are **not required** (the probe
  downloaded without them).
- **Resume:** `07` from offset N returns the file's bytes from N on, exactly. The size in the
  reply is still the **whole** file's size, not what remains.

### Unsolicited events (on `0011203a`)

- `03 <name>` — recording started
- `3b 00 00` — repeats while recording
- `10` — recording stopped
- `04 <name><u32 size>` — a new recording was saved

## Audio format

A recording file has **no container**: it is back-to-back **80-byte frames**, each one Opus
packet of a 20 ms frame at 16 kHz mono. So 50 packets per second, 4000 bytes per second, and
`size / 4000` is the duration the app shows.

- Each packet begins with the Opus TOC byte `0x4b` (config 9 = SILK wideband, 20 ms; one frame).
- Short packets are padded to 80 bytes with Opus's own code-3 padding (a run of trailing zero
  bytes), which is why most packets start with `4B 41` (`"KA"`, the app's name for the format,
  "KA80").
- **Damaged packets happen on the device.** Three of the five recordings seen had one packet cut
  short; from there the 80-byte boundaries shift until the next packet start is found. The app
  does the same re-alignment. This is in the stored data, not a transfer error (downloads were
  byte-complete). A decoder (and our importer) must re-align: skip to the next 80-byte slot that
  reads as a valid Opus packet and is itself followed by one.

To make a playable file without an Opus decoder, wrap the packets in an **Ogg Opus** stream:
an `OpusHead` (1 channel, 16 kHz, pre-skip 0) and `OpusTags` header, then one Ogg packet per
80-byte frame, granule position advancing 320 samples each. knowpod does this when importing
(`desktop/src/airec-protocol.js`).

## Cloud (not needed to read recordings)

The app uploads the Opus to `https://usa.jnnrec.com:8388` (OSS storage, `ai_transcription/*`
endpoints) with a per-account bearer token, and binds the recorder by serial through
`device/getDeviceInfoByBleAlias` and `device/verify_code`. None of this gates the Bluetooth link;
it is the vendor's own transcription pipeline. knowpod does not use it.

## Open questions

- What `0f`, `22`, `26`'s fields, `36`, `66`, `69` mean. `airec-probe --explore` probes these.
- Whether `34 00 01` is required before `0a` to delete, or just what the app happens to send.
  `airec-probe --allow-write --delete <name> [--no-34]` tests this.
- Whether storage is really in MB.

## Security

This recorder has **no link-level authentication**. Within Bluetooth LE range (a few metres), any
device can connect — when the owner's phone isn't already connected — and read or delete the
owner's recordings. The account "pairing" is cloud-side only. This is a real privacy weakness in
the product. Test only recorders you own. It is a candidate for coordinated disclosure to the
vendor (jnnrec.com).
