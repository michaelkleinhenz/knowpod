# pocket-wifi-probe

A research tool for finding out how a Pocket recorder (firmware **1.8**) transfers files over
WiFi. It raises the recorder's access point over Bluetooth, joins it, runs a set of scans and
tests, then puts the recorder and this machine's WiFi back the way they were. Everything that
happens goes into a JSON report.

How the AP is raised comes from pocket-libre's
[PROTOCOL.md](https://github.com/shahcolate/pocket-libre/blob/main/PROTOCOL.md). The steps
after that are not decoded yet, and finding them is what this tool is for. The tool refuses
to run on firmware other than 1.8 unless you pass `--force`, because the other command order
can leave the recorder unreachable until it's power-cycled.

## Findings so far

Everything learned so far is in [RESEARCH.md](RESEARCH.md): the recorder's details, the AP
sequence, port 8475, what the Android app's strings reveal, the working theory of the
transfer, the next steps, and the plan for the desktop app. Keep it up to date after each run.


## Capturing the official app (`capture_app.py`)

To see what the official Android app does, run this on the laptop with the phone on adb (USB
debugging on, the Pocket app installed, the Bluetooth HCI snoop log enabled):

```sh
python3 capture_app.py snoop-check       # first: does the live Bluetooth log come through?
python3 capture_app.py activation        # the app activating (setting up) a recorder
python3 capture_app.py quick-transfer    # the app's Quick Transfer (Wi-Fi)
```

It needs `adb`, `iw`, `tcpdump`, `nmcli` and sudo (`--no-wifi`: only `adb`), and uses only the
standard library. The script tells you when to do what in the app. While it runs, type a note
and Enter at each step in the app ("tapped Add device") to put a marker in the timeline; type
`done` when finished.

- **Bluetooth:** it streams the phone's HCI snoop log from its btsnoop socket (`adb forward` from
  a free laptop port to the phone's 8872) and decodes it live: the recorder's advertising (any name matching `--device-name`,
  default `^PKT`), connections, pairing (SMP) and encryption, the GATT layout as the app
  discovers it, and every ATT read, write, subscription, notification and indication, labelled
  with the characteristic's UUID. Text values like `APP&`/`MCU&` show as text, others as hex. At
  the end it takes a bug report for the on-phone snoop log, in case the socket isn't available.
- **WiFi:** it puts the laptop's WiFi card in monitor mode and hops the 2.4 GHz channels until
  it sees the recorder: its known AP MAC (`--bssid`) or any MAC with its WiFi OUI (`--oui`,
  default `a4:c1:38`), so a recorder never seen before is found too. It then stays on that
  channel and records everything, including WPA handshakes. It logs the networks the recorder
  announces or looks for (probe requests), which shows whether it's given home WiFi details.
- **App log:** `adb logcat`. For `activation` it prints every line from the app's process.
- It saves the phone's Bluetooth state (`dumpsys bluetooth_manager`) before and after, so
  bonds that appear show up.

`snoop-check` tests the live log on its own: it forwards a port, reads the btsnoop header and
shows packets for 20 s (`--restart` restarts the phone's Bluetooth first). The capture runs the
same check. A working forward isn't proof by itself: `adb forward` accepts the connection even
when nothing listens on the phone, so only a btsnoop header counts. The socket serves one client
at a time, so close Wireshark's androiddump and other forwards to 8872 first.

Output goes to `capture-<scenario>-<time>/` (gitignored; it can contain the session key and
WiFi passwords): `timeline.log` (everything printed, unredacted, with full values),
`phone-live.btsnoop`, `*.decoded.txt` (every btsnoop log decoded with all details),
`wifi-monitor.pcap`, `logcat.txt` and the bug report. The app's own internet traffic (for
example to its cloud during activation) is TLS from the phone and isn't captured; logcat is
the window into it.

To decode a btsnoop log again, or one from elsewhere, without a phone:

```sh
python3 capture_app.py decode capture-…/phone-live.btsnoop          # --all for every detail
```

## What a run does

1. Connects over Bluetooth and unlocks with the session key (`APP&SK&…`). Reads the firmware
   version and stops if it isn't 1.8.
2. Runs the **ble** checks.
3. Remembers the WiFi network this machine is on, then runs the firmware 1.8 sequence:
   `U&WIFI` → `WIFI` (SSID and password) → stage the newest recording (`U&<date>&<ts>`) →
   `WIFIO`. The probe joins the AP while it polls `WIFIS`; the AP is only up for seconds.
4. Starts the **monitors** and runs the **ready** checks.
5. Sends `APP&U&WIFI` (the command the app uses to start the transfer) and runs the **begin**
   checks. `--no-begin` skips this step.
6. Teardown, which always runs, including after Ctrl-C: stops the monitors, sends `WIFIC`,
   rejoins the original WiFi network, deletes the temporary profile and disconnects Bluetooth.

## Testing how the recorder takes its session key (`pocket-keybind`)

After a factory reset, use this to find out how the recorder decides which session key to
accept (see RESEARCH.md). It uses this machine's Bluetooth only (no WiFi, no phone):

```sh
pocket-keybind F4:4B:26:17:34:1D lzYjgY68j5fmiwAM      # or set POCKET_SESSION_KEY
```

It runs a few connections in turn. Each one optionally sends a single `APP&SK&<key>`, then
sends `APP&BAT` and reports whether the recorder answered (the real sign of being unlocked) and
whether it dropped the link. The steps are `--steps` (default `none,key,fresh,key`):

| step          | what it sends |
|---------------|---------------|
| `none`        | no key: does the recorder answer commands unlocked? |
| `key`         | the key on the command line / `POCKET_SESSION_KEY` (on a reset recorder: the activation) |
| `fresh`       | a freshly generated 16-char key, not the command-line one |
| `other:<KEY>` | a specific key, e.g. the one the vendor app used before the reset |

Each step opens its own connection, like the app does. Unlocking makes the recorder raise its WiFi AP, and while that AP is up it stops advertising over Bluetooth for ~15 s, so the next step can't be found at once. The tool handles this by dropping the AP (`WIFIC`) before disconnecting, retrying the scan (`--scan-attempts`, default 4) and waiting between steps (`--between`, default 15 s). If a step still ends in 'Recorder not found', give it longer: `--between 25 --scan-timeout 30`.

At the end it prints a plain-language reading (first-key-wins, any key works, or not locked).
Keys are redacted from the output and left out of the `--out` JSON report. Run it only against a
recorder you own; for a clean first-key test, factory reset it first.

## Install and run

Needs Python 3.11 or newer, and Bluetooth plus WiFi on the same machine.

```sh
cd tools/pocket-wifi-probe
python -m venv .venv && . .venv/bin/activate      # Windows: .venv\Scripts\activate
pip install -e .

export POCKET_SESSION_KEY=...                     # Windows: set POCKET_SESSION_KEY=...
pocket-wifi-probe AA:BB:CC:DD:EE:FF
```

You can also pass the key as the second argument. The environment variable keeps it out of
your shell history.

| OS      | Joins with                          | Notes |
|---------|-------------------------------------|-------|
| Linux   | NetworkManager (`nmcli`)            | For the packet capture (`capture` monitor), allow tcpdump once: `sudo setcap cap_net_raw,cap_net_admin=eip $(which tcpdump)`. |
| Windows | `netsh wlan`                        | Allow Python through the firewall when Windows asks, or the `inbound` monitor sees nothing. For packets, run Wireshark on the WiFi adapter alongside. |
| other   | `--wifi manual`: you join by hand   | The probe prints the SSID and password and waits until it has an address on the AP. |

Useful options:

- `--pause`: wait for Enter before teardown, so you can poke around by hand.
- `--hold 60`: keep the AP up 60 seconds longer.
- `--checks tcp-scan,tcp-probe` / `--skip capture`: choose what runs. `--list-checks` lists them.
- `--tcp-ports 1-1024`: a shorter sweep. The default is every port.
- `--recording 2026-09-03/20260903145856`: stage a specific recording instead of the newest.
- `--notify 001120a1,…`: after unlocking, also subscribe to these characteristics (UUIDs or
  prefixes) and log their notifications. `001120a1` carries Bluetooth audio.
- `--notify-all`: the same for every notify characteristic. The recorder dropped the link while
  `ffd2` was subscribing, so prefer `--notify`.

The report (`pocket-probe-<time>.json`) holds the timeline, with every Bluetooth message
in and out, the WIFIS history, and each check's result. The session key and WiFi password are
replaced with `<redacted>`.

## Checks

| Name        | Runs in      | What it does |
|-------------|--------------|--------------|
| `stream`    | ready (first)| keeps trying to connect to the transfer socket (`--stream-port`, 8475) until it opens, sends `--stream-trigger` over Bluetooth (default: `U&<date>&<ts>` for the longest recording, then `U&WIFI` 0.3 s later, as the official app does), and saves whatever arrives to `<report>.8475.bin` |
| `ble-info`  | ble          | battery, WiFi firmware, storage, state, USB mode |
| `gatt`      | ble          | GATT table, with the value of every readable characteristic |
| `ble-state` | ready, begin | `WIFIS`/`STE`, and the WIFIS history so far |
| `net-info`  | ready, begin | address, DHCP lease, routes, ARP neighbours |
| `ping`      | ready, begin | ICMP echo |
| `tcp-scan`  | ready, begin | TCP connect sweep. Marks the sweep unreliable if it ran out of sockets. |
| `tcp-probe` | ready, begin | sends each opening line in `PAYLOADS` (HTTP, `RANGE` guesses, …) to every open port and port 53 |
| `udp-probe` | ready, begin | datagrams to `--udp-ports`, separating replies from ICMP "closed" |
| `dns`       | ready        | queries the recorder's DNS server over UDP and TCP, plus `version.bind` |
| `discovery` | ready, begin | mDNS, SSDP and subnet broadcast |
| `inbound`   | monitor      | TCP and UDP listeners on `--listen-ports`, in case the recorder connects to us |
| `capture`   | monitor      | `tcpdump` of the WiFi interface to a `.pcap` (Linux, root) |

## Adding a check

Add a module under `pocket_wifi_probe/checks/` (it's picked up automatically), or add a
function to an existing one:

```python
from . import check, monitor

@check("my-check", phases=("ready", "begin"), help="one line for --list-checks")
async def my_check(ctx):
    ctx.log("my-check", "shown live and kept in the timeline")
    return {"anything": "JSON-serialisable"}        # stored in the report

@monitor("my-monitor", help="runs in the background while on the AP")
async def my_monitor(ctx, stop):
    await stop.wait()
    return {...}
```

`ctx` gives you:

- `ctx.host`: the recorder at `192.168.200.1`.
- `ctx.local_ip`: this machine's address on the AP.
- `ctx.pocket`: the Bluetooth link. `await ctx.pocket.request("BAT", "BAT")` returns the
  value, and `send`/`wait_for` give lower-level access.
- `ctx.wifi`: the OS WiFi backend.
- `ctx.args`: the command-line options.
- `ctx.shared`: results earlier checks left for later ones, such as `open_tcp`.
- `ctx.report.meta`: run facts, such as `staged` and `ssid`.

New guesses at the transfer protocol can go straight into `PAYLOADS` in `checks/tcp.py`,
`DATAGRAMS` in `checks/udp.py` or `DNS_NAMES`, without writing a new check.

Run the tests with `pip install -e '.[test]' && pytest`. `tests/test_session.py` runs the
whole flow against a fake 1.8 recorder on localhost.
