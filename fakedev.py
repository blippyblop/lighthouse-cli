import os, pty, select, signal, subprocess, sys, time

master, slave = pty.openpty()
slave_name = os.ttyname(slave)

RESP = {
    b"id": [b"name Lighthouse Base Station", b"serial SN0001", b"fw fpga7-0.9", b"radio build 2025.10"],
    b"journal": [b"journal clear"],
    b"journal list": [b"no journal entries"],
    b"param list laser": [b"laser.pwr.m 0.3", b"laser.pwr.gain 4", b"laser.pwr 50",
                          b"laser.pwr.b1 1.5", b"laser.pwr.b2 -3.2",
                          b"laser.pwr.detected 1", b"laser.pwr.average 12"],
    b"param save": [b"params saved"],
    b"factory save-cal": [b"calibration saved"],
    b"reboot": [b"rebooting"],
}

import pathlib
argv = [str(pathlib.Path(__file__).with_name("lighthouse-cli")), "-v"] + sys.argv[1:]
for i, a in enumerate(argv):
    if i >= 2 and a not in ("cmd","set","save","save-cal","reboot","flash","status","log","sniff","monitor"):
        argv[i] = slave_name
        break

LONGRUN = ("log", "sniff", "monitor")
cmdname = sys.argv[1] if len(sys.argv) > 1 else ""

print(f"[harness] slave={slave_name} child={' '.join(argv)}", flush=True)
proc = subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
buf = b""

def pump():
    global buf
    data = os.read(master, 65536)
    buf += data
    while True:
        idxs = [buf.find(s) for s in (b"\r", b"\n")]
        idxs = [i for i in idxs if i >= 0]
        if not idxs:
            break
        i = min(idxs)
        line = buf[:i].strip()
        buf = buf[i+1:]
        if not line:
            continue
        key = line.decode(errors="ignore").strip().encode()
        print(f"[dev]  RX: {key.decode()!r}", flush=True)
        if key.startswith(b"param set "):
            resp = [b"param set " + key[11:] + b" ok"]
        else:
            resp = RESP.get(key, [b"ack " + key])
        for r in resp:
            os.write(master, r + b"\r")
            print(f"[dev]  TX: {r.decode()!r}", flush=True)

deadline = time.time() + 25
sigint_at = time.time() + 8 if cmdname in LONGRUN else None
while proc.poll() is None and time.time() < deadline:
    if sigint_at is not None and time.time() > sigint_at:
        proc.send_signal(signal.SIGINT)
        sigint_at = None
    r, _, _ = select.select([master], [], [], 0.2)
    if master in r:
        pump()
if proc.poll() is None:
    proc.kill()
out, _ = proc.communicate()
print("===== lighthouse-cli output =====")
sys.stdout.write(out.decode(errors="replace"))
print("===== exit", proc.returncode, "=====")
os.close(master); os.close(slave)