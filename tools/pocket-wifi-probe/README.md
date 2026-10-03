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
- `--notify-all`: also log notifications from the other Bluetooth characteristics.

The report (`pocket-probe-<time>.json`) holds the timeline, with every Bluetooth message
in and out, the WIFIS history, and each check's result. The session key and WiFi password are
replaced with `<redacted>`.

## Checks

| Name        | Runs in      | What it does |
|-------------|--------------|--------------|
| `stream`    | ready (first)| keeps trying to connect to the transfer socket (`--stream-port`, 8475) until it opens, sends `--stream-trigger` over Bluetooth (default: `U&<date>&<ts>` for the longest recording, then `WIFI&SWITCH`), and saves whatever arrives to `<report>.8475.bin` |
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
