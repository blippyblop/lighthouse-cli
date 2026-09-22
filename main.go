package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

const (
	version = "0.3.0"

	vidLighthouse = "28DE"
	pidLighthouse = "2500"

	cmdID       = "id"
	cmdJournal  = "journal"
	cmdJrnlList = "journal list"
	cmdPollLsr  = "param list laser"
	cmdParamSv  = "param save"
	cmdSaveCal  = "factory save-cal"
	cmdReboot   = "reboot"
	cmdEepromW  = "eeprom w 0 1344"
)

type lineCh struct {
	line string
	ts   time.Time
}

type session struct {
	p    serial.Port
	out  chan lineCh
	lf   *os.File
	verb bool
}

func usage() {
	fmt.Print(`lighthouse-cli - headless Lighthouse base station console

usage:
  lighthouse-cli scan                       list serial ports, highlight VID 28DE / PID 2500
  lighthouse-cli status <port>              connect, bootstrap (id / journal / journal list),
                                      print "param list laser" once
  lighthouse-cli monitor <port>             connect, bootstrap, then poll "param list laser"
                                      every 500ms until Ctrl-C
  lighthouse-cli log <port>                 connect, bootstrap, then capture all RX until Ctrl-C
  lighthouse-cli sniff <port>               open port, capture all RX (no TX) until Ctrl-C
  lighthouse-cli cmd <port> <line>...       send raw line(s), print responses until quiet
  lighthouse-cli set <port> <key> <value>   "param set <key> <value>"
  lighthouse-cli save <port>                "param save"
  lighthouse-cli save-cal <port>            "factory save-cal"
  lighthouse-cli reboot <port>              "reboot"
  lighthouse-cli flash <port> <b64file>     "eeprom w 0 1344" + each b64 line + "reboot"
  lighthouse-cli version                    print version

known param keys:
  laser.pwr.m      float  0.1 .. 0.5
  laser.pwr.gain   int    0 .. 7
  laser.pwr        int    0 .. 100
  laser.pwr.b1     float  0 .. 10
  laser.pwr.b2     float -20 .. 20

flags (before subcommand):
  -v        echo raw TX/RX to stdout
  -l <file> tee raw TX/RX (timestamped) to file

transport: 115200 8N1, frames are ASCII lines terminated by "\r" (RX accepts "\r" or "\n")
`)
}

func openPort(name string) (serial.Port, error) {
	mode := &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	}
	return serial.Open(name, mode)
}

func (s *session) raw(dir, data string) {
	ts := time.Now().Format("15:04:05.000")
	if s.verb {
		fmt.Printf("%s %s %s\n", ts, dir, data)
	}
	if s.lf != nil {
		fmt.Fprintf(s.lf, "%s %s %s\n", ts, dir, data)
	}
}

func (s *session) start() {
	go func() {
		buf := make([]byte, 4096)
		var acc strings.Builder
		for {
			n, err := s.p.Read(buf)
			if n > 0 {
				for _, b := range buf[:n] {
					if b == '\r' || b == '\n' {
						line := strings.TrimSpace(acc.String())
						acc.Reset()
						if line == "" {
							continue
						}
						s.raw("RX <-", line)
						s.out <- lineCh{line: line, ts: time.Now()}
					} else {
						acc.WriteByte(b)
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

func (s *session) send(line string) {
	s.raw("TX ->", line)
	if _, err := s.p.Write([]byte(line + "\r")); err != nil {
		fmt.Fprintf(os.Stderr, "write %q: %v\n", line, err)
	}
}

func (s *session) drain(quiet, maxWait time.Duration) []string {
	var got []string
	deadline := time.Now().Add(maxWait)
	for {
		wait := quiet
		if d := time.Until(deadline); d < wait {
			wait = d
		}
		if wait <= 0 {
			break
		}
		select {
		case l := <-s.out:
			got = append(got, l.line)
		case <-time.After(wait):
			return got
		}
	}
	return got
}

type kv struct {
	key string
	val string
}

var laserKeys = map[string]bool{
	"laser.pwr":          true,
	"laser.pwr.m":        true,
	"laser.pwr.gain":     true,
	"laser.pwr.b1":       true,
	"laser.pwr.b2":       true,
	"laser.pwr.detected": true,
	"laser.pwr.average":  true,
}

func parseKV(lines []string) []kv {
	var out []kv
	for _, ln := range lines {
		ln = strings.TrimPrefix(ln, ":")
		fields := strings.Fields(ln)
		if len(fields) == 0 {
			continue
		}
		key := fields[0]
		key = strings.Trim(key, ",;:|")
		val := strings.TrimSpace(ln[len(fields[0]):])
		val = strings.Trim(val, ",;:|")
		out = append(out, kv{key: key, val: val})
	}
	return out
}

func printKV(lines []string, onlyLaser bool) {
	for _, e := range parseKV(lines) {
		if onlyLaser && !laserKeys[e.key] {
			continue
		}
		fmt.Printf("  %-22s %s\n", e.key, e.val)
	}
}

func showRaw(lines []string) {
	for _, ln := range lines {
		fmt.Printf("  | %s\n", ln)
	}
}

func (s *session) roundTrip(line string) []string {
	s.send(line)
	return s.drain(700*time.Millisecond, 5*time.Second)
}

func (s *session) pollLaser() []string {
	s.send(cmdPollLsr)
	return s.drain(300*time.Millisecond, 3*time.Second)
}

func (s *session) bootstrap() {
	for _, c := range []string{cmdID, cmdJournal, cmdJrnlList} {
		fmt.Printf("== %s ==\n", c)
		showRaw(s.roundTrip(c))
	}
}

func watchCtrlC(s *session) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		fmt.Println("\n[Ctrl-C] closing")
		s.p.Close()
		if s.lf != nil {
			s.lf.Close()
		}
		os.Exit(0)
	}()
}

func newSession(name string) *session {
	p, err := openPort(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", name, err)
		os.Exit(1)
	}
	s := &session{p: p, out: make(chan lineCh, 256), verb: verbose, lf: logf}
	s.start()
	return s
}

func doScan() {
	list, err := enumerator.GetDetailedPortsList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "enumerate: %v\n", err)
		os.Exit(1)
	}
	if len(list) == 0 {
		fmt.Println("no serial ports found")
		return
	}
	found := false
	for _, pi := range list {
		vid := strings.ToUpper(pi.VID)
		pid := strings.ToUpper(pi.PID)
		mark := "   "
		if pi.IsUSB && vid == vidLighthouse && pid == pidLighthouse {
			mark = ">>>"
			found = true
		}
		fmt.Printf("%s %-14s vid=%-5s pid=%-5s product=%s\n", mark, pi.Name, pi.VID, pi.PID, pi.Product)
	}
	if !found {
		fmt.Printf("\nno matching USB serial ports found (looking for vid %s pid %s)\n", vidLighthouse, pidLighthouse)
	}
}

func doStatus(name string) {
	s := newSession(name)
	defer s.p.Close()
	s.bootstrap()
	fmt.Println("== param list laser ==")
	printKV(s.roundTrip(cmdPollLsr), true)
}

func doMonitor(name string) {
	s := newSession(name)
	watchCtrlC(s)
	s.bootstrap()
	fmt.Println("== param list laser (polling every 500ms, Ctrl-C to stop) ==")
	period := 500 * time.Millisecond
	for {
		start := time.Now()
		lines := s.pollLaser()
		fmt.Printf("[%s]\n", time.Now().Format("15:04:05.000"))
		printKV(lines, true)
		if d := period - time.Since(start); d > 0 {
			time.Sleep(d)
		}
	}
}

func doLog(name string) {
	s := newSession(name)
	watchCtrlC(s)
	s.bootstrap()
	fmt.Println("capturing RX until Ctrl-C")
	for {
		select {
		case l := <-s.out:
			fmt.Printf("[%s] %s\n", l.ts.Format("15:04:05.000"), l.line)
		}
	}
}

func doSniff(name string) {
	s := newSession(name)
	watchCtrlC(s)
	fmt.Printf("sniffing %s until Ctrl-C\n", name)
	for {
		select {
		case l := <-s.out:
			fmt.Printf("[%s] %s\n", l.ts.Format("15:04:05.000"), l.line)
		}
	}
}

func doCmd(name string, cmds []string) {
	s := newSession(name)
	defer s.p.Close()
	for _, c := range cmds {
		fmt.Printf("== %s ==\n", c)
		showRaw(s.roundTrip(c))
	}
}

func doSet(name, key, val string) {
	s := newSession(name)
	defer s.p.Close()
	lines := s.roundTrip(fmt.Sprintf("param set %s %s", key, val))
	showRaw(lines)
	fmt.Println("== param list laser ==")
	printKV(s.roundTrip(cmdPollLsr), true)
}

func doSimple(name, line string) {
	s := newSession(name)
	defer s.p.Close()
	fmt.Printf("== %s ==\n", line)
	showRaw(s.roundTrip(line))
}

func doVersion() {
	fmt.Printf("lighthouse-cli %s\n", version)
}

func doFlash(name, file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", file, err)
		os.Exit(1)
	}
	s := newSession(name)
	watchCtrlC(s)
	s.send(cmdEepromW)
	time.Sleep(300 * time.Millisecond)
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		s.send(ln)
		time.Sleep(50 * time.Millisecond)
	}
	s.send(cmdReboot)
	time.Sleep(2 * time.Second)
	s.p.Close()
	if s.lf != nil {
		s.lf.Close()
	}
	fmt.Println("Done!")
}

var (
	verbose bool
	logf    *os.File
)

func main() {
	args := os.Args[1:]
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-v":
			verbose = true
			args = args[1:]
		case "-l":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "-l requires a file argument")
				os.Exit(2)
			}
			var err error
			logf, err = os.Create(args[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "create %s: %v\n", args[1], err)
				os.Exit(2)
			}
			verbose = true
			args = args[2:]
		default:
			fmt.Fprintf(os.Stderr, "unknown flag %s\n", args[0])
			usage()
			os.Exit(2)
		}
	}
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "scan":
		doScan()
	case "status":
		needArgs(rest, 1)
		doStatus(rest[0])
	case "monitor":
		needArgs(rest, 1)
		doMonitor(rest[0])
	case "log":
		needArgs(rest, 1)
		doLog(rest[0])
	case "sniff":
		needArgs(rest, 1)
		doSniff(rest[0])
	case "cmd":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "usage: lighthouse-cli cmd <port> <line>...")
			os.Exit(2)
		}
		doCmd(rest[0], rest[1:])
	case "set":
		if len(rest) != 3 {
			fmt.Fprintln(os.Stderr, "usage: lighthouse-cli set <port> <key> <value>")
			os.Exit(2)
		}
		doSet(rest[0], rest[1], rest[2])
	case "save":
		needArgs(rest, 1)
		doSimple(rest[0], cmdParamSv)
	case "save-cal":
		needArgs(rest, 1)
		doSimple(rest[0], cmdSaveCal)
	case "reboot":
		needArgs(rest, 1)
		doSimple(rest[0], cmdReboot)
	case "flash":
		if len(rest) != 2 {
			fmt.Fprintln(os.Stderr, "usage: lighthouse-cli flash <port> <b64file>")
			os.Exit(2)
		}
		doFlash(rest[0], rest[1])
	case "version":
		doVersion()
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func needArgs(rest []string, n int) {
	if len(rest) < n {
		fmt.Fprintf(os.Stderr, "not enough arguments (need %d)\n", n)
		os.Exit(2)
	}
}
