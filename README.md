# lighthouse-cli

> **WARNING**
> This tool can be used to set your lighthouse to use parameters that can
> damage the device. Use at your own risk, this can be used to break your
> lighthouse.

A headless command-line console for the Lighthouse base station over its USB
serial port. It finds connected base stations, reads device identity and laser
parameters, adjusts laser parameters, saves configuration to the device,
reboots it, and can rewrite base calibration data.

## TL;DR

```sh
lighthouse-cli scan                        # find the base station (VID 28DE / PID 2500)
lighthouse-cli status /dev/ttyUSB0         # connect + read laser parameters once
lighthouse-cli set /dev/ttyUSB0 laser.pwr 80     # change a parameter
lighthouse-cli reboot /dev/ttyUSB0         # restart the device
```

More commands and flags in Usage below.

## AI disclaimer

This project is made with heavy use of AI LLM models. This section is human
written, but the rest is basically all AI.

## Just give me the exe, nerd

If you want an exe, go to releases at the right panel and select the latest
one. Run it in powershell or cmd or something. If you've never used a CLI tool
before now is a good opportunity to learn!

## Contents

| file | what it is |
|---|---|
| `main.go` | the whole tool (single file) |
| `go.mod`, `go.sum` | module `lighthouse-cli`, dep `go.bug.st/serial v1.6.4` |
| `PROTOCOL.md` | serial protocol documentation |
| `fakedev.py` | test harness: simulated device on a pty pair |
| `.github/workflows/release.yml` | CI: cross-builds the binary on version tags and attaches it to the GitHub release |
| `LICENSE` | MIT |

## Build from source

Prerequisites:

- **Go** >= 1.21 (any recent release works; the tool is a single file with one
  dependency that Go fetches automatically)
- **Linux:** access to the serial device. Either add your user to the `dialout`
  group, or drop in a udev rule so `/dev/ttyUSB*` is open without root:

  ```
  # /etc/udev/rules.d/99-lighthouse.rules
  SUBSYSTEM=="tty", ATTRS{idVendor}=="28de", ATTRS{idProduct}=="2500", MODE="0666"
  ```

  then `sudo udevadm control --reload-rules` and replug.
- **macOS:** no extra setup; the serial port belongs to the logged-in user.
- **Windows:** no extra setup; the port shows up as `COMx`.

Build:

```sh
go build -o lighthouse-cli .
```

Verify it works without any hardware:

```sh
python3 fakedev.py cmd /dev/null id "param list laser"
```

The harness runs the built binary against a simulated device on a pseudo-terminal
and should exit 0.

Cross-compiling:

```sh
CGO_ENABLED=0 GOOS=linux   go build -o lighthouse-cli-linux .
CGO_ENABLED=0 GOOS=windows go build -o lighthouse-cli.exe .
# darwin needs cgo and a macOS host:
GOOS=darwin                go build -o lighthouse-cli-darwin .   # on a Mac
```

Prebuilt binaries for Linux, macOS, and Windows are also attached to each
release (GitHub Releases page) and are built automatically by CI on version
tags.

## Usage

```
lighthouse-cli scan                        list ports, mark VID 28DE / PID 2500
lighthouse-cli status <port>               bootstrap + one "param list laser" read
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

Transport: USB-serial CDC, 115200 8N1; ASCII lines, TX terminated by `\r`, RX
accepts `\r` or `\n`. The base station is the USB CDC device VID `28DE` /
PID `2500`.

## Typical usage

A normal session, start to finish. Commands below use Linux port names; on
Windows use `COMx` and on macOS `/dev/cu.usbserial-*` — everything else is
identical.

### 1. Plug it in and find the port

Plug the lighthouse's USB cable into the machine. It enumerates as a USB CDC
serial device (VID `28DE` / PID `2500`); find which port it landed on:

```sh
lighthouse-cli scan
```

```
>>> /dev/ttyUSB0   vid=28DE  pid=2500  product=...
    /dev/ttyACM0   vid=0403  pid=6001  product=...
```

Lines marked `>>>` are lighthouses — use that port name (`/dev/ttyUSB0` here)
for everything that follows. If no line is marked, the unit is probably not
powered, the cable is only charging, or you need the permissions setup from
the build notes.

### 2. Connect and confirm it's the right unit

```sh
lighthouse-cli status /dev/ttyUSB0
```

This opens the port at 115200 8N1, sends the bootstrap commands (`id`,
`journal`, `journal list`), then reads the laser parameters once. The `id`
response includes the device name and serial number — check that serial against
the label on the unit to make sure you're talking to the lighthouse you think
you are (there are more cross-checks in "Finding the right device" below).

Example session (from the simulated-device harness included with this repo):

```
== id ==
  | name Lighthouse Base Station
  | serial SN0001
  ...                       # journal / journal list output elided
== param list laser ==
  laser.pwr              50
  laser.pwr.m            0.3
  laser.pwr.gain         4
  laser.pwr.detected     1
  laser.pwr.average      12
```

Real devices report their own lines — if anything looks different, record a
trace (step 6) to see exactly what the unit sends.

### 3. Make a change and watch it apply

Pick a key from the "Known parameter keys" table and a value inside its range:

```sh
lighthouse-cli set /dev/ttyUSB0 laser.pwr 80
```

The tool sends `param set laser.pwr 80`, waits for the device's reply, then
re-reads the parameter list so you can see the new value take effect. Repeat
with as many keys as you like — each `set` is independent.

### 4. Make it stick

Values changed with `set` are live settings on the device. Save them so they
survive a power cycle or reboot:

```sh
lighthouse-cli save /dev/ttyUSB0
```

### 5. Wrapping up

- `lighthouse-cli reboot /dev/ttyUSB0` — restart the unit (also a handy
  "that's the one" check, since it visibly power-cycles).
- `lighthouse-cli save-cal /dev/ttyUSB0` — factory save-calibration.
- `lighthouse-cli flash /dev/ttyUSB0 <payload-file>` — rewrite base
  calibration data (advanced; see `PROTOCOL.md`). Power-cycles the device when
  done.

### 6. When it misbehaves

No responses, garbled lines, or something you don't recognise? Rerun with `-v`
to watch the raw TX/RX traffic, or with `-l` to also record it to a file:

```sh
lighthouse-cli -v status /dev/ttyUSB0
lighthouse-cli -l trace.log status /dev/ttyUSB0
```

## Finding the right device

With several lighthouses on the network, confirm you're talking to the one you
think you are before you touch its params:

- **Match the serial number.** `lighthouse-cli cmd <port> id` returns the
  device's identity, including its serial number. Compare it against the serial
  number printed on the device itself to line the port up with the physical
  unit.
- **Reboot to verify.** `lighthouse-cli reboot <port>` restarts the unit — if
  the lighthouse you're looking at powers down and back up (status LED blinks
  while it boots, then settles green), that's the port you just rebooted.

**Ground truth on real hardware:** `lighthouse-cli -l trace.log status /dev/ttyUSB0`
drives the device while recording every raw TX/RX line.

**Verify without hardware:**

```sh
python3 fakedev.py cmd /dev/null id "param list laser"
python3 fakedev.py status /dev/null
```

The harness answers from a pty slave, so the tool's framing, drain, parsing,
and dispatch are all exercised end to end.

## Known parameter keys

| key | type | range |
|---|---|---|
| `laser.pwr.m` | float | 0.1 … 0.5 |
| `laser.pwr.gain` | int | 0 … 7 |
| `laser.pwr` | int | 0 … 100 |
| `laser.pwr.b1` | float | 0 … 10 |
| `laser.pwr.b2` | float | −20 … 20 |

## Changelog

- **0.1.1** — `status` now does a single parameter read and exits instead of
  polling once a second; docs updated to match.
- **0.1.0** — First release. Untested.