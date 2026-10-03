# Pocket WiFi transfer: research notes

Everything learned so far about how a Pocket recorder (heypocketai.com) sends recordings over
WiFi. It's written so someone (or a new Claude session) can pick up the work with no other
context. Last updated 2026-10-03. The work is on branch `ccr-a77c2b23-v3g3ci`, in PR #95.

## Why

The goal is a WiFi alternative to USB sync in the knowpod desktop app (Electron, `desktop/`).
It would be offered in the same dialog as "activate USB", Linux and Windows first. The app
already talks to the recorder over Bluetooth (`desktop/src/bluetooth.js`,
`desktop/src/pocket-bluetooth.js`), and already copies files over USB (`desktop/src/pocket.js`).

Turning the recorder's WiFi access point (AP) on over Bluetooth is understood. What runs on
the AP was not, and that blocks the feature. `pocket-wifi-probe` (this directory) is the tool
for finding out. The design for the desktop side is under
[Plan for the desktop app](#plan-for-the-desktop-app).

## Sources

- pocket-libre, [PROTOCOL.md](https://github.com/shahcolate/pocket-libre/blob/main/PROTOCOL.md)
  and its issue #4 (as of 2026-09-19, v1.1.0). It has the Bluetooth protocol and the
  firmware 1.8 command order for the AP. It says WiFi transfer "does not currently work on any
  firmware" and that its author found no endpoint on the AP. Two of its claims turned out wrong
  or incomplete for our recorder (see below).
- Three probe runs on 2026-10-03 against the user's recorder, from a Linux laptop on
  NetworkManager.
- `strings` output from the Android app's `libapp.so`.

## The recorder

| | |
|---|---|
| Name | `PKT01_GREY_261717ff` |
| Bluetooth address | `F4:4C:26:17:17:FF` |
| Firmware | `1.8` (`APP&FW`) |
| WiFi firmware | `V9` (`APP&WF`) |
| AP MAC | `a4:c1:38:81:90:50` |
| Storage | `059629&059634` KB used/total (`APP&SPACE`) |

The probe and the desktop app support **firmware 1.8 only**. On 1.8, the AP sequence written
down for 1.3.3 never brings the AP up. Bluetooth then drops within seconds, and the recorder
needs a physical power-cycle. `APP&WIFIC` can't fix that, because Bluetooth is already gone.

## Bluetooth protocol (what we rely on)

- The command channel is characteristic `001120a3-2233-4455-6677-889912345678` in service
  `001120a0-…`, with write-without-response and notify. The app writes ASCII `APP&<cmd>` and
  the recorder answers `MCU&<cmd>&<value>` as notifications on the same characteristic. The
  desktop app and pocket-libre both use this characteristic. PROTOCOL.md's table points at
  `e49a3002/3`, which also exist.
- A connection must first be unlocked with `APP&SK&<16-char session key>` → `MCU&SK&OK`.
  The **first 8 characters of the session key are the AP's WPA2 password**. Treat the key as a
  secret: the desktop app encrypts it with `safeStorage`, and the probe redacts it from reports.
- **Every notification arrived about 4 times**, within a few milliseconds. The probe drops
  repeats under 50 ms apart; one run dropped 182. It's not yet known whether the recorder
  sends them that way or BlueZ duplicates them.
- Other GATT services: `e49a25f8` (audio notify `e49a28e1`), `e49a3001`, `ffd0` (vendor
  UART), battery `180f`, `001120a1` (notify; audio for Bluetooth downloads). `001120a2` is
  write.
- `APP&LIST_DIRS` → `MCU&DIRS&<date>` … `MCU&DIRS_SUM&<n>`. `APP&LIST&<date>` →
  `MCU&F&<date>&<timestamp>&<duration seconds>` … `MCU&LIST&<n>`. The last field is a
  duration, not a size.

## The AP sequence on firmware 1.8 (works, both runs)

The same order pocket-libre found works. Times are seconds into the run (run 1 / run 2):

```
>> APP&U&WIFI                            no answer (normal on 1.8)
>> APP&WIFI
<< MCU&WIFIS&0                           3.1 / 3.7
<< MCU&WIFI&PKT01_GREY_261717ff&<password>
>> APP&U&2026-10-02&20261002224747       pick ("stage") a recording BEFORE raising the AP
<< MCU&U&2486                            its size in bytes
<< MCU&OFF                               right after U, not after WIFIO as PROTOCOL.md says
>> APP&WIFIO
<< MCU&WIFIO
<< MCU&WIFIS&3   AP up                   3.2 / 6.2
<< MCU&WIFIS&2   client connecting       11.3 / 12.0
<< MCU&WIFIS&1   ready                   15.3 / 18.0
>> APP&WIFIC                             cleanup; Bluetooth stayed up and the recorder was fine
```

Joining the AP from the laptop takes about 10 s after `WIFIO`. NetworkManager joins it fine
with a profile marked hidden (`802-11-wireless.hidden yes`), WPA2-PSK, `ipv4.never-default
yes`. The AP's network:

- The recorder is `192.168.200.1`, and the laptop gets `192.168.200.2/24` by DHCP (lease
  7200 s). There's no default route. DHCP names `192.168.200.1` as the DNS server, but port 53
  is **closed** on our recorder (UDP and TCP), unlike pocket-libre's report.
- ICMP echo works, at about 1 ms.

## The transfer socket: TCP 8475

**Run 1 (14:57):** joined on the first try. It ran every check: ble-info, gatt, full TCP
sweep, payload probes, UDP, DNS, discovery.

- In a full 1–65535 sweep, **only 8475 was open**. It was found about 6 s after joining.
  Every other port was closed (RST) or gave no answer. On UDP, only 67 (DHCP) didn't answer
  with ICMP unreachable.
- A connection that sent nothing was accepted and stayed **silent for 2 s without closing**.
  So the recorder waits for the client.
- The next connection sent `\r\n` and **got a reset**. After that, every connect to 8475 was
  **refused**.
- After `APP&U&WIFI` (the old "begin" step, no Bluetooth answer), a new sweep found 8475
  **open again**. Twenty seconds later the probes were refused again, before any of them had
  sent anything.
- Nothing connected back to the laptop: 58 TCP and 58 UDP listeners heard nothing. mDNS,
  SSDP and broadcast got no replies.

**Run 2 (15:19):** the stream check, sweeps and a packet capture. **8475 never opened.**

- The capture shows only RST and ICMP unreachable from the recorder, plus one ARP reply. It
  sent nothing on its own.
- Likely cause: the first `nmcli --wait 15 connection up` timed out at 19.2 s, but the link
  was actually coming up (`WIFIS` was 2 at 12.0 s and 1 at 18.0 s). The retry ran
  `connection up` again, which **disconnects and reconnects**. The working theory is that **the
  recorder closes the socket once its first client leaves**. The join code was fixed to never
  re-issue a connect while one is activating.
- The other difference from run 1: no `ble-info` before WiFi, so no `APP&WF` query.

**Run 3:** running with the join fix and the old `U&WIFI` trigger. Results pending.

## What the Android app reveals

The package is `com.heypocket.app`. It's **Flutter**: the code is in `libapp.so` in the
`split_config.arm64_v8a.apk` split, and it's **obfuscated** (names like `_ozl@576048475`). The
`8475` inside that name is a coincidence. A port would be compiled in as a number, so it won't
show up in `strings`.

Findings from `strings`:

- WiFi transfer is a **framed stream**: "Pocket Wi-Fi frame", "Invalid Pocket Wi-Fi frame
  length:", "Pocket Wi-Fi framed stream ended with", "Unhandled frame type", "Pocket Wi-Fi
  packet stream already has an active consumer", "Pocket Wi-Fi transfer buffer exceeded",
  "Pocket Wi-Fi transfer socket is closed", "Reading from a closed socket".
- **`RANGE` is a Bluetooth thing**, not WiFi. The `RANGE request bytes=` / `RANGE complete
  received=` / `RANGE cancel generation=` strings sit beside "Byte-range downloads are
  Bluetooth-only.", "Bluetooth byte-range downloads cannot switch to Wi-Fi.", "Device firmware
  does not support Bluetooth byte-range downloads." and "Requested byte range starts beyond the
  end of the device file." pocket-libre's guess that `RANGE` is the WiFi verb is wrong.
  `APP&U&<date>&<ts>&<…>` (a third field) is probably the Bluetooth byte-range request.
- **Switching a transfer to WiFi:** `APP&WIFI&SWITCH` → `MCU&WIFI&SWITCH`. Related lines:
  "Switching file transfer to Wi-Fi.", "Device rejected the Wi-Fi transfer switch.", "Device did
  not stop the Bluetooth file transfer for Wi-Fi handoff.", "Pocket Wi-Fi is no longer
  connected before transfer switch.", "Pocket device disconnected during Wi-Fi file transfer."
  So **a WiFi transfer starts as a Bluetooth transfer (`APP&U&<date>&<ts>`) and is switched
  over.** `MCU&OFF` likely marks the end of a Bluetooth transfer. Our staged file (2486 bytes)
  had already gone out over Bluetooth before `WIFIO`.
- **Heartbeat:** `APP&WPING` → `MCU&WPING`. Related lines: "Device Wi-Fi heartbeat failed.",
  "Disabling device Wi-Fi heartbeat after three consecutive failures.", "Stopping device Wi-Fi
  heartbeat."
- **Prewarm:** the app raises the AP ahead of time ("WiFi prewarm", "Device did not acknowledge
  Wi-Fi prewarm.", "Device Wi-Fi is unavailable for prewarm.") and then hands the prepared
  transfer to a request ("Prepared Pocket Wi-Fi transfer was already claimed.").
- **Several files per socket:** "Multiple file download with same wifi socket", "Previous
  Wi-Fi file completed after the next file request started.", "Device repeatedly completed the
  previous Wi-Fi file during the next file request."
- `MCU&WIFIS&1&` appears with a trailing field in the app's parser. Our recorder sends a plain
  `MCU&WIFIS&1`.
- Requirements in the UI: battery above 10% ("Charge your Pocket above 10% to use Wi-Fi
  transfer"), and location permission on the phone. "Quick Transfer (Wi-Fi)" is the app's name
  for it. "Sync Normally (Bluetooth)" is the alternative.
- Integrity: "Complete checksum mismatch …" / "Partial checksum mismatch …" (frames or files
  carry checksums). MP3 checks: "Not enough MP3 frames to verify constant bitrate", an
  `Mp3FrameParser`.
- Firmware note in the app: "this firmware has no APP&SCHED. It was added by the 2026-09-20
  re-cut of 1.8.5, which reports the same version as the 2026-09-18 build that lacks it".
  Builds can differ while reporting the same version.

The commands found in the app (`APP&…` / `MCU&…`):

| Group | Commands |
|---|---|
| AP / transfer | `U&WIFI`, `WIFI` (credentials), `WIFIO` (on), `WIFIS` (status), `WIFIC` (close), `WIFI&SWITCH`, `WPING`, `WIFID`, `WIFIE`, `WIFIM&0`, `WIFIX` (app waits 60 s; `MCU&WIFIX&PENDING`) |
| Home-WiFi upload (recorder joins your router; not needed here) | `WIFIJ` (join, 30 s), `WIFIL` (list, `…&END`), `WSCAN`, `WIFIP&<secret>` / `WIFIP&CLR`, `WIFI&CH&<secret>`, `UPL&…`, `UPAUTH`, `AUTOUP`, `SYNC&…`, `SCHED&…`, `MCU&URL&`, `MCU&S3&…` |
| Firmware | `WOTA`, `OTA&…`, `OTA&WIFI&…`, `MCU&W91&RESETW` |
| Other | `BAT`, `FW`, `WF`, `SPACE`, `STE` (0/1/2), `MAC`, `T&<time>`, `GET&USB`, `USB&`, `LIST_DIRS`, `LIST&`, `D&` (delete?), `REC&SECEN`, `PAU`, `RESU`, `STA`, `STO`, `SHUT`, `BLE&OFF`, `BLE&RESET`, `LNP&`, `LNS&`, `LOG&…` |

Only the first row matters for Quick Transfer. Don't send the firmware commands. Leave the
untested ones (`WIFIM`, `WIFIX`, `WIFIE`, `WIFID`) for when the order is known: any of them
could change the recorder's WiFi settings.

## Working theory of the app's Quick Transfer

1. Raise the AP: `U&WIFI` → `WIFI` → `U&<date>&<ts>` → `WIFIO`, then wait for `WIFIS` 1.
2. Join the AP and connect to `192.168.200.1:8475`. Send nothing on that connection.
3. Keep `APP&WPING` going.
4. Start a Bluetooth transfer of the file (`APP&U&<date>&<ts>`), then send `APP&WIFI&SWITCH`.
   Expect `MCU&WIFI&SWITCH`, after which the recorder sends length-prefixed frames on the socket.
5. For the next file, send another `APP&U&…` and switch again on the **same socket**.
6. Finish with `APP&WIFIC`.

## The probe today

`pocket-wifi-probe` is the tool in this directory; see [README.md](README.md) for usage and
for adding checks. What it does now:

- Joins without ever re-issuing a connect mid-activation (NetworkManager: the profile's
  `GENERAL.STATE`; netsh: reconnects only once the interface reports disconnected).
- **`stream`** (runs first):
  1. Retries connecting to 8475 for `--stream-wait` (30 s). A refusal is harmless.
  2. Listens 2 s before triggering anything.
  3. Sends `--stream-trigger`. The default is `U&{date}&{ts},WIFI&SWITCH`, with the
     **longest** recording, so a Bluetooth transfer is still running when the switch arrives.
  4. Reads until 10 s of silence, closing, or 120 s.
  5. Saves the bytes to `<report>.8475.bin`, and reports MP3 sync offsets and any header
     fields equal to a known size.
- `--heartbeat N` sends `APP&WPING` every N s while on the AP.
- `capture` writes a pcap. tcpdump needs `sudo setcap cap_net_raw,cap_net_admin=eip
  $(which tcpdump)` once.
- Reports go to `pocket-probe-<time>.json`, with the key and password redacted. They're
  gitignored.

**Next run to do:**

```sh
pocket-wifi-probe F4:4C:26:17:17:FF --heartbeat 2 --checks stream,ble-info,ble-state,net-info,capture
```

Check for `joined … on attempt 1`, `MCU&WIFI&SWITCH`, and "first bytes arrived". Then:

- **If bytes arrive:** decode the frame layout from the `.bin` (length field, type field,
  checksum; MP3 frames start with `FF F3`). Then write a `download` path and check that the
  joined payload matches the file.
- **If the switch is rejected or nothing arrives**, try these in order:
  - Different trigger orders, for example `WIFI&SWITCH` alone, or `U&…` sent before
    connecting.
  - Whether 8475 opens at all without `ble-info` beforehand, to rule `APP&WF` in or out.
  - Decompiling `libapp.so` with [blutter](https://github.com/worawit/blutter) (needs
    `libflutter.so` from the same split). Look at the code around "Invalid Pocket Wi-Fi frame
    length:" and the `WIFI&SWITCH` sender to get the frame header, the port constant, and any
    hello the app sends.
  - Capturing the official app doing a Quick Transfer: put the laptop's Intel card in monitor
    mode on the AP's channel. Wireshark can decrypt WPA2-PSK with the known password if it sees
    the phone's 4-way handshake.

## Open questions

- What makes 8475 open, how long it stays open, and whether it allows only one client.
- Whether `WIFI&SWITCH` needs a Bluetooth transfer in progress, and whether audio
  notifications must be subscribed (`001120a1`).
- The frame format (header, type values, length width and byte order, checksum) and how a file
  ends.
- What `MCU&WIFIS&1&<x>` carries on firmware that sends it.
- Whether the 4× notification copies come from the recorder or from BlueZ.

## Plan for the desktop app

Once the protocol is known, the desktop app gets a "Transfer over WiFi" choice in the dialog
that has "activate USB":

- **Bluetooth part.** Extend `desktop/src/bluetooth.js` (runs in a hidden window, Web
  Bluetooth) with the AP sequence, the heartbeat and the switch. The session key stays in the
  main process (`safeStorage`).
- **WiFi switching.** A new main-process module calling OS tools through `execFile`, like
  `desktop/src/pocket.js`. The code in `pocket_wifi_probe/hostwifi.py` already does the parts
  that are known to work:
  - Remember the current connection (`nmcli -t -f NAME,UUID,TYPE,DEVICE connection show
    --active` / `netsh wlan show interfaces`, which uses English or German labels).
  - Create a temporary hidden-network profile *before* `WIFIO`. On Windows that's a WLAN
    profile XML with `nonBroadcast`, added with `user=current`.
  - Join without re-issuing a connect mid-activation.
  - Always restore the original network and delete the profile.
- **Edge cases.** The laptop has no internet while on the AP, so download locally first and
  upload after. On Ethernet, WiFi can join the AP while internet stays on the wire. Check that
  the battery is above 10%.
- **macOS later.** The current SSID can no longer be read from the command line since Sonoma,
  so it needs a CoreWLAN helper or relies on auto-rejoin.
