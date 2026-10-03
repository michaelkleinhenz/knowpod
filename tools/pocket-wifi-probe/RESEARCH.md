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

**Run 3 (15:35):** the join fix, `ble-info` first, the stream check with the default trigger
(`U&<longest>` then `WIFI&SWITCH` 1 s later), heartbeat every 2 s, and a capture.

- Joined **on the first attempt**, about 12 s after `WIFIO`. `WIFIS` went 0 → 3 → 2 → 1.
- **8475 accepted on the first try**, 0.5 s after `WIFIS=1`. This supports the run 2 theory
  that reconnecting mid-join is what closed it.
- The connection stayed **silent for 14 s**, and the recorder sent no bytes at all. When we
  closed it, it answered with a clean FIN (no RST). The recorder just waits.
- `APP&U&2026-10-02&20261002084958` → `MCU&U&1114450`, then **`MCU&OFF` 0.16 s later**. 1.1 MB
  can't go over Bluetooth in 0.16 s. So `MCU&OFF` doesn't mean "transfer finished". It means
  the Bluetooth transfer **stopped or never started**. The probe was subscribed only to the
  command characteristic, not to `001120a1` (the audio channel). The likely cause is that the
  recorder won't stream without a subscriber.
- `APP&WIFI&SWITCH` → **`MCU&WIFI&<ssid>&<password>`**, not `MCU&WIFI&SWITCH`. That's the
  reply to plain `APP&WIFI`. The firmware either prefix-matched `APP&WIFI`, or this is how it
  answers a switch when no Bluetooth transfer is running. The app expects one to be running
  ("Device did not stop the Bluetooth file transfer for Wi-Fi handoff").
- The capture had nothing else from the recorder. Most packets were the laptop's own DNS
  queries to `192.168.200.1:53` (about 1,000, all refused). That's systemd-resolved using the
  DHCP-supplied DNS, which is harmless but noisy.

**Runs 4 and 5 (15:41, 15:43):** `--notify-all` before unlocking. Run 4 dropped within 4 s,
before `SK` was sent. With subscriptions moved after `SK`, run 5 subscribed to `001120a1`,
`e49a3003` and `2a05`. It hung on `ffd2` (vendor UART) and the recorder dropped the
connection while that was pending. **Don't subscribe to `ffd2`.** Use `--notify 001120a1`.

**Run 6 (15:44):** `--notify 001120a1 --stream-gap 0.3`. **Bluetooth download works; the
switch doesn't.**

- **Bluetooth download format:** after `APP&U&<date>&<ts>` → `MCU&U&<size>`, the recorder sends
  the file's **raw bytes** as `001120a1` notifications of 244 bytes (some 192), with no header.
  The last notification is short, then `MCU&OFF`. The staged file arrived as 10×244 + 46 = 2486
  bytes, exactly `MCU&U`. So **`MCU&OFF` = end of the Bluetooth transfer**. In run 3 it came at
  once only because nothing was subscribed.
- **The files are MP3**, MPEG-2 Layer III, 16 kHz, 32 kbit/s (header `FF F3 48 C4`, 144-byte
  frames). 1,114,450 bytes / 4000 B/s = 278 s, which is the duration `MCU&F` lists. So
  `MCU&U` is the size in bytes and `MCU&F`'s last field is seconds.
- **Bluetooth throughput:** about 26 KB/s (about 110 notifications/s), so 1.1 MB takes about
  45 s. That's the speed WiFi has to beat.
- **`APP&WIFI&SWITCH` sent 0.3 s into a running Bluetooth transfer** still got
  `MCU&WIFI&<ssid>&<password>`. The Bluetooth transfer **kept going** and the 8475 socket stayed
  silent (the pcap shows only the handshake and our FIN). The firmware treats `WIFI&SWITCH` as
  `APP&WIFI`: **our firmware 1.8 / WiFi V9 doesn't implement the switch.**
- `APP&U&WIFI` sent mid-transfer got `MCU&U&WIFI` and then `MCU&U&1114450` again. The audio
  stopped 1.2 s later without an `MCU&OFF`, and the next `APP&STE` got no answer.
- The probe drops repeats under 50 ms apart per characteristic. Audio chunks are all different
  so far, but a download path must not dedup audio. Count bytes against `MCU&U` instead.

**Capture 1 of the official app (16:02, `capture_quick_transfer.py`):** app 0.6.86+5929 on
Android. The phone's live btsnoop socket worked, and the bug report had the same log. **The
app never used WiFi:**

- On connecting, it subscribes (CCCD writes to handles `0x0031` and `0x002e`; command
  notifications come on `0x0030`, audio on `0x002d`, commands are written to `0x002b`). Then:
  `SK` → `BAT`, `FW`, `GET&USB`, `MAC`, `SPACE`, `WF` (several times each) → `REC&SECEN`
  (→ `MCU&REC&CON`) → `T&<yyyymmddhhmmss UTC>` (→ `MCU&T&OK`) → `STE` → `LIST&<date>` for each
  of the last 7 days and the next 2 (it doesn't use `LIST_DIRS`).
- 12 s after the app started, a background Flutter engine **auto-synced the new 23 s recording
  over Bluetooth**: `APP&U&<date>&<ts>` → `MCU&U&92062` → 382 audio notifications → `MCU&OFF`.
  That took 1.4 s, about 65 KB/s, 2.5× the laptop's rate. It then uploaded the file to the
  cloud. Around this the app held a high-performance WiFi lock (16:03:28–16:03:34) but sent no
  WiFi command, and the monitor never saw the AP.
- So no recording was left un-imported for Quick Transfer. The next capture needs recordings
  the app doesn't auto-sync first.
- Each `MCU&` reply appears **once** in the phone's HCI log. The 4× copies on the laptop come
  from BlueZ or the probe, not the recorder.
- The app's own Flutter logging doesn't reach logcat (only Shorebird, BackgroundTransfer and
  other plugin lines do), so logcat is of little use.

**Capture 2 (16:16):** a 180 s recording made while the phone's Bluetooth was off, so it
wasn't auto-synced. The app connected with the same startup sequence. When the user opened the
import, it sent `APP&LIST_DIRS` and listed both days. 4.6 s later it fetched the recording
**over Bluetooth**: `APP&U&2026-10-03&20261003141332` → `MCU&U&722348` → 2998 notifications in
11 s (65 KB/s) → `MCU&OFF`. Again there was **no WiFi command** (`WIFI`, `WIFIO`, `WIFI&SWITCH`)
and no AP. The only WiFi-side effect was the same WiFi lock plus "Wifi Latency mode" on the
phone. So **with firmware 1.8 / WiFi V9, the official app doesn't use WiFi either**, at least
in this flow.

The user tapped **Quick Transfer (Wi-Fi)**. The app said to tap "Accept" when the phone asks to
join the Pocket's WiFi, but that system prompt never appeared. After a while it fell back to
Bluetooth. The app reports it's on the current firmware. In the 30 s between connecting and
the fallback, the app sent **nothing over Bluetooth** and made **no WiFi network request**
(no `WifiNetworkSpecifier` in logcat). It gave up before doing anything, so a guard in the app
failed. The phone (Pixel, Android 17, app 0.6.86 targetSdk 36) shows:

- `NEARBY_WIFI_DEVICES` **not granted** (never requested). Fine and coarse location, local
  network and all Bluetooth permissions are granted.
- OpenVPN is the phone's configured VPN app, but no VPN network was connected.
- **Likely cause: recording size.** The app contains "Bluetooth is used for smaller
  recordings", "`. Recordings will sync over Bluetooth instead.`", `belowThreshold` /
  `below_threshold`, `thresholdSeconds`, `allowBluetoothFallback` and `wifi_transfer_bytes`.
  So Quick Transfer silently uses Bluetooth below some threshold. The value is a compiled or
  remote-config constant, not visible in the strings. Our captures used 23 s (92 KB) and 180 s
  (722 KB). Next: a recording of 20+ minutes (about 5 MB at 4 KB/s).
- The app queried `APP&WF` four times at connect, which suggests it checks the WiFi
  firmware version. "BLE WiFi OTA requires MCU T22+ and WiFi V10+" shows it compares
  versions, and ours is V9.

**Capture 3 (17:17): the official app doing a real Quick Transfer.** A 44:36 recording
(`20261003143216`, 2676 s, 10,705,002 bytes). The phone's HCI log has the whole sequence
(times are seconds after the app opened the import, UTC 15:18:15):

```
0.0  >> APP&LIST_DIRS / << MCU&DIRS… / MCU&F…            the app lists the recordings
0.7  >> APP&WIFIS          << MCU&WIFIS&0
0.9  >> APP&WIFIO          << MCU&WIFIO                   no U&WIFI and no WIFI before it
1.0  >> APP&WPING          << MCU&WPING                   heartbeat, then every 10 s
3.5  >> APP&WIFI           << MCU&WIFI&<ssid>&<password>
3.6  >> APP&WIFIS (every 1 s) << MCU&WIFIS&3 ×4, then &2 at 7.8
7.8  the phone asks Android for the network (WifiNetworkSpecifier, SSID PKT01_…)
52.1 the phone joins (2462 MHz = channel 11; 45 s of that is Android finding it),
     DHCP gives 192.168.200.2
55.1 >> APP&WIFIS          << MCU&WIFIS&1                 client on the AP
55.3 >> APP&U&<date>&<ts>  << MCU&U&10705002              Bluetooth transfer starts (audio on 0x002d)
55.5 >> APP&WIFIS          << MCU&WIFIS&1
55.6 >> APP&U&WIFI                                         0.35 s after U: switch to WiFi
56.9 (last Bluetooth audio)  << MCU&U&WIFI  << MCU&U&10705002
70.8 << MCU&OFF                                            10.7 MB done: about 770 KB/s
70.8 >> APP&WIFIC          << MCU&WIFIC
```

- **The switch is `APP&U&WIFI`, sent during a running Bluetooth transfer.** `WIFI&SWITCH`
  isn't used on this firmware. `MCU&U&WIFI` is the acknowledgement, followed by the size again.
  `MCU&OFF` marks the end of the file, over WiFi too. About 13 KB had already gone over
  Bluetooth, so the WiFi stream either resumes or starts over; the socket data will show which.
- **WIFIS meanings, corrected:** 0 = off, 3 = AP starting, 2 = AP up and waiting for a client,
  1 = a client has joined.
- The phone joined on a **second WiFi interface (`wlan1`)**, Android's local-only connection,
  and stayed on its home WiFi on `wlan0`. That's why the user saw no WiFi switch.
- Bluetooth stayed connected throughout, with `WPING` every 10 s.
- **Run 6 of the probe did the switch without knowing it:** `APP&U&WIFI` mid-transfer got
  `MCU&U&WIFI` and `MCU&U&1114450`, and the Bluetooth audio stopped. But the stream check had
  already closed the 8475 socket 0.4 s earlier, so nothing was there to receive the file.
- The laptop's monitor capture caught almost nothing (147 frames, none from the AP). We don't
  need it now: the probe can be the WiFi client itself.

**Run 7 (17:27): WiFi transfer works from the laptop.** `--no-begin`, stream check with the
new defaults (`U&<longest>` then `U&WIFI` 0.3 s later, `001120a1` subscribed):

```
17.9  connected to 192.168.200.1:8475 (first try), silent
19.9  >> APP&U&2026-10-03&20261003143216   << MCU&U&10705002   (Bluetooth audio starts)
20.2  >> APP&U&WIFI
21.4  << MCU&U&WIFI  << MCU&U&10705002    first bytes on 8475 at the same moment
31.1  << MCU&OFF                          last bytes on 8475
41.1  no more data for 10 s; socket still open
```

- **10,705,012 bytes in 9.7 s: about 1.1 MB/s**, 40× the laptop's Bluetooth rate.
- **No framing.** The stream is the raw MP3 file from byte 0. It restarts rather than resuming
  after the Bluetooth bytes: it begins `FF F3 48 C4 00 00 00 03 48 …`, like the Bluetooth
  stream. MPEG-2 L3 frames (144 bytes) follow back to back to the end with no gap, so nothing
  is inserted mid-stream. (The app's "Pocket Wi-Fi frame" code may be for newer firmware.)
- **10 extra bytes at the end**, arriving as their own TCP segment just before `MCU&OFF`:
  `ba 5a 02 8f 04 ba 5a 02 8f 04`. That's not CRC32, Adler-32, a byte sum or XOR of the file,
  and not the size. For now: **take exactly `MCU&U` bytes and treat `MCU&OFF` as the end.**
- The recorder didn't close the socket after the file. The app's "Multiple file download with
  same wifi socket" suggests the next file reuses it.

**Runs 8 and 9 (17:35, 17:36): integrity.** The same recording (`20261003142550`, 885,788
bytes) over Bluetooth only (`U&…,wait:OFF`, 38 s) and over WiFi (`U&…,U&WIFI,wait:OFF`).

- **The WiFi bytes match the Bluetooth file exactly**, all 885,788 of them.
- The **10 bytes after the file are the same again: `ba 5a 02 8f 04 ba 5a 02 8f 04`**, for a
  different file. So it's a fixed end marker, not a checksum. The WiFi stream is `<size bytes
  of file><ba5a028f04 ×2>`, and `MCU&OFF` comes over Bluetooth just after it.
- WiFi: `U&WIFI` → first byte 1.3 s, 885 KB in 1.4 s. Again the socket stayed open after
  the file.
- The laptop's Bluetooth stream had 3 extra 227-byte chunks that repeat earlier data (a BlueZ
  artifact the 50 ms dedup doesn't catch). Without them it matches. That doesn't affect WiFi.
- The first try at run 10 didn't start: the recorder dropped Bluetooth twice while connecting,
  about 30 s after run 9. It worked after a pause.

**Run 10 (17:39): three files, one socket.** `U&<f>,U&WIFI,wait:OFF` three times on the same
connection. **Only the first file arrived** (885,788 bytes + marker, identical to run 9). For
files 2 (722,348) and 3 (92,062) the recorder answered as usual (`MCU&U&WIFI`, then `MCU&OFF`
after 1.2 s and 0.7 s, at WiFi speed), but **no byte came on the open connection**. Next: a new
connection per file (`reconnect` step), with a capture to see where the data goes.

**Run 11 (17:41): a new connection per file, old ones left open.** Connection 1 got file 1
(+ marker). `reconnect` before files 2 and 3 was accepted, but **neither got a byte**. The third
connection was **reset 3 ms after the handshake**, so it seems at most two connections at once.
The pcap shows **no TCP data from the recorder at all after file 1**, while it still reported
`MCU&U&WIFI` and `MCU&OFF` within about a second. Nobody closed connection 1. Next: close it
before reconnecting (`close` step). If that fails: a full `WIFIC`/`WIFIO` cycle per file, or
the client has to answer the end marker.

**Run 12 (17:43): close, then reconnect.** `…,wait:OFF,sleep:1,close,sleep:2,reconnect,…`

- **File 2 arrived on the new connection**: 722,348 bytes + the same marker. So the
  rule so far is **one connection per file, closed by the client after the marker**. When we
  close (FIN), the recorder closes its side at once.
- After the first close, 8475 refused connections (RST to SYN, so no listener) for about
  3.5 s, then accepted again.
- After the **second** close it **never listened again** (30 s of refusals). Run 11 also
  allowed only two connections. So **at most two connections per AP session** on this
  firmware, at least as used here.
- File 3 was then requested anyway: `U&` → `MCU&U&92062`, `U&WIFI` → `MCU&U&WIFI`, but with no
  client there was no `MCU&OFF`. 14 s later the recorder sent **`MCU&SHUT`** (new). Bluetooth
  stayed up, and `WIFIC` was answered normally at the end. **Never send `U&WIFI` without an
  open connection.**

**Run 13 (17:50): restarting the AP resets the limit. All three files arrived.**
`file1, close, reconnect, file2, cycle, reconnect, file3`, where `cycle` = close, `WIFIC`,
`nmcli connection down`, 2 s, `WIFIO`, rejoin, wait for `WIFIS=1`.

- File 1 (885,788) and file 2 (722,348) are byte-identical to the copies from runs 9 and 12.
  File 3 (92,062) is identical to the Bluetooth copy. Each is followed by the 10-byte marker.
- `cycle` took **14.2 s**: `WIFIC` → `WIFIS&0` → `WIFIO` → `3` (2 s) → `2` (8 s) → laptop
  joined → `1` (12 s). After it, 8475 accepted at once, and file 3 arrived 1.3 s after
  `U&WIFI`.
- `WIFIO` worked straight after `WIFIC`, with no `U&WIFI` and no staged `U&` first, as the app
  does it.

## The WiFi transfer protocol (firmware 1.8 / WiFi V9), as worked out

1. **Bluetooth:** unlock with `SK`. Subscribe to `001120a1` (audio). The Bluetooth transfer that
   gets switched ends at once without a subscriber.
2. **Raise the AP:** `WIFIO` (→ `MCU&WIFIO`), `WIFI` (→ SSID and password; the password is the
   first 8 characters of the session key), then poll `WIFIS` about once a second: 3 = starting,
   2 = waiting for a client, 1 = client joined. Send `WPING` every few seconds (the app: 10 s).
   The SSID is hidden; join it as a hidden WPA2-PSK network. DHCP gives 192.168.200.2, and the
   recorder is 192.168.200.1.
3. **Per file:**
   1. Connect to `192.168.200.1:8475` and send nothing. If refused, retry every 0.5 s: after
      a close it refuses for about 1.5–3.5 s.
   2. `APP&U&<date>&<ts>` → `MCU&U&<size>` (the Bluetooth transfer starts).
   3. About 0.3 s later `APP&U&WIFI` → `MCU&U&WIFI`, `MCU&U&<size>` (about 1.2–1.5 s later).
   4. Read `<size>` bytes of raw MP3 from the socket, then a fixed 10-byte marker
      `ba 5a 02 8f 04 ba 5a 02 8f 04`. `MCU&OFF` arrives over Bluetooth at the same time.
      Speed: about 0.7–1.1 MB/s.
   5. Close the connection (the recorder closes its side at once).
4. **At most two connections per AP session.** After the second, 8475 stops listening. For more
   files, restart the AP: `WIFIC`, disconnect, `WIFIO`, rejoin, `WIFIS=1` (about 14 s).
   **Never send `U&WIFI` without an open connection**: the recorder hangs and later sends
   `MCU&SHUT`.
5. **End:** `WIFIC` (→ `MCU&WIFIC`), then rejoin the usual network.

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
  3. Sends `--stream-trigger`. The default is `U&{date}&{ts},U&WIFI` with a 0.3 s gap, like
     the app, for the **longest** recording. `--notify` defaults to `001120a1`; without a
     subscriber the recorder ends the Bluetooth transfer at once.
  4. Reads until 10 s of silence, closing, or 120 s.
  5. Saves the bytes to `<report>.8475.bin`, and reports MP3 sync offsets and any header
     fields equal to a known size.
- `--heartbeat N` sends `APP&WPING` every N s while on the AP.
- `capture` writes a pcap. tcpdump needs `sudo setcap cap_net_raw,cap_net_admin=eip
  $(which tcpdump)` once.
- Reports go to `pocket-probe-<time>.json`, with the key and password redacted. They're
  gitignored.

**Next:** build a `download` command into the probe using the protocol above (several
files, two per AP session, MD5 against `MCU&U` sizes and the marker), then the desktop app's
"Transfer over WiFi" (see the plan below).

## Open questions

- Why the AP stops listening after two connections, and whether reusing one connection works
  if the client answers the end marker somehow (the app's "Multiple file download with same
  wifi socket").
- How the app's Quick Transfer works on firmware that has it. Our 1.8 / V9 answers
  `WIFI&SWITCH` like `APP&WIFI`, even mid-transfer.
- What `MCU&WIFIS&1&<x>` carries on firmware that sends it.

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
