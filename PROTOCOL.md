# Lighthouse Base Station — Serial Protocol

A compact serial protocol for the Lighthouse base station, worked out from the
device and public information. `lighthouse-cli` is a small headless
implementation of it.

## 1. Transport & framing

| property | value |
|---|---|
| interface | USB-serial CDC, 115200 baud, 8 data bits, no parity, 1 stop |
| USB filter | **VID `28DE`, PID `2500`** (base station) — the port scan uppercases and matches the 4-hex pair |
| encoding | ASCII lines |
| TX terminator | `\r` (0x0D) appended to every command |
| RX framing | line ends on `\r` **or** `\n` (0x0A); whitespace-trimmed; empty lines dropped |
| serial stack | `go.bug.st/serial v1.6.4` |

Every exchange is: host sends `"<command>\r"`, device replies with one or more
`"<line>\r"` lines. The host drains until RX goes quiet (700 ms quiet / bounded
wait per command).

## 2. Commands

Exact command strings:

| command | context | purpose |
|---|---|---|
| `id` | connect bootstrap | device identity (name / serial / FPGA code version / radio build / model) |
| `journal` | connect bootstrap | journal control |
| `journal list` | connect bootstrap | list journal entries |
| `param list laser` | 1 s poll, refresh, post-set re-poll | laser telemetry + current params |
| `param set <key> <value>` | param edit (debounced) | set a parameter |
| `param save` | manual save | persist parameters to device |
| `factory save-cal` | manual save-cal | factory save-calibration |
| `reboot` | manual reboot / end of cal rewrite | reboot base station |
| `eeprom w 0 1344` | base-cal rewrite | start EEPROM write: offset 0, 1344 bytes |

Notes:
- **FPGA firmware update** (`fpga6.bin` / `fpga7.bin`, 128 KB each) has **no
  serial command**: it is a manual USB mass-storage procedure (power off → hold
  button → copy the .bin to the device's USB drive → power on). Nothing is sent
  over the serial link.

### 2.1 Calibration rewrite

Sequence for a base-cal rewrite (the confirm payload text is the calibration
data):

1. send `eeprom w 0 1344`
2. send each line of the payload text (84 lines; 1344 bytes of calibration
   data, hex-encoded)
3. send `reboot`
4. wait ~2 s, disconnect, show "Done!"

## 3. Parameters (`param set` / `param list laser`)

Settable keys (type, range):

| key | type | range |
|---|---|---|
| `laser.pwr.m` | float | 0.1 … 0.5 |
| `laser.pwr.gain` | int | 0 … 7 |
| `laser.pwr` | int | 0 … 100 |
| `laser.pwr.b1` | float | 0 … 10 |
| `laser.pwr.b2` | float | −20 … 20 |

Wire format: `param set <key> <value>` (value as decimal text; floats use
`%s` of the formatted number, ints `%d`).

Telemetry keys in the `param list laser` response:

| key | meaning |
|---|---|
| `laser.pwr` | output power setting |
| `laser.pwr.m` | power multiplier |
| `laser.pwr.gain` | gain index |
| `laser.pwr.b1` / `laser.pwr.b2` | bias / balance trims |
| `laser.pwr.detected` | beam/laser detected flag |
| `laser.pwr.average` | average power reading |

Device-info fields (populated from `id`-class responses):
Device Name, Serial Number, FPGA Code Version, Radio Build, OOTX Model.
Exact response line format for `id`/`journal` is best confirmed on real
hardware (the tool tees raw traffic, see §5).

## 4. Connection & polling flow

1. On **Connect**: open port @115200 8N1, start the RX reader, send bootstrap
   `id`, `journal`, `journal list`, then a `param list laser` read for telemetry.
2. **Param edits**: 200 ms debounce → `param set <key> <value>` for each
   changed field → re-poll `param list laser`.
3. **Refresh**: immediate `param list laser`.
4. **Save Params**: `param save`. **Save Cal**: `factory save-cal`.
   **Reboot**: `reboot` (+ follow-up).
5. **Base-cal rewrite**: §2.1 sequence.
6. On **Disconnect**: stop reader, close port.

### 4.1 Lighthouse sweeps

A lighthouse "sweep" pattern is a device config/state item whose trigger was
not pinned down statically. If lighthouse behavior matters, capture it on
hardware (§5).

## 5. Tool (`lighthouse-cli`)

Build: `go build -o lighthouse-cli .` (needs Go ≥1.21; dep:
`go.bug.st/serial v1.6.4`).

```
lighthouse-cli scan                        list ports, mark VID 28DE / PID 2500
lighthouse-cli status <port>               bootstrap + one "param list laser" read
lighthouse-cli monitor <port>              bootstrap, then poll "param list laser"
                                           every 500ms until Ctrl-C
lighthouse-cli log <port>                  bootstrap, then raw RX capture (Ctrl-C stops)
lighthouse-cli sniff <port>                open + raw RX only (spontaneous traffic)
lighthouse-cli cmd <port> <line>...        send raw line(s), print responses
lighthouse-cli set <port> <key> <value>    param set + verify via param list laser
lighthouse-cli save <port>                 param save
lighthouse-cli save-cal <port>             factory save-cal
lighthouse-cli reboot <port>               reboot
lighthouse-cli flash <port> <payloadfile>  eeprom w 0 1344 + each line + reboot

flags: -v (echo raw TX/RX), -l <file> (tee timestamped raw trace to file)
```

**Ground truth on real hardware:**
`lighthouse-cli -l trace.log status /dev/ttyUSB0` (or `COMx` on Windows) —
this drives the device with the standard command cadence while recording every
raw TX/RX line. If any response format assumption in §3 is off, the trace shows
the true lines.

**Verified without hardware:** framing, port open, command dispatch, line
parsing, and all subcommands were exercised against a simulated
device on a pty pair (`fakedev.py`): the tool sent `id` / `param list
laser` / `param set laser.pwr.m 0.4` etc. and correctly framed, drained,
parsed, and exited.