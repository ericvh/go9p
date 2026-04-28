package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/lionkov/go9p/cmd/internal/cli9p"
	"github.com/lionkov/go9p/p/clnt"
)

func dief(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "error: "+format+"\n", args...)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  %s discover [flags]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s devices  [flags]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s meta     -id <device-id> [flags]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s status   -id <device-id> [flags]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s value    -id <device-id> -name <value-name> [flags]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s call     -id <device-id> -fn <function> [-payload <text>|-payload-file <path>|-stdin] [flags]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nConnection flags:\n")
	fmt.Fprintf(os.Stderr, "  -net tcp|unix  -addr 127.0.0.1:5640  -msize 8192  -dotu  -aname \"\"  -d 0\n")
	fmt.Fprintf(os.Stderr, "\nFilesystem flags:\n")
	fmt.Fprintf(os.Stderr, "  -root /devices\n")
}

func cmdDiscover(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf cli9p.ConnFlags
	cf.Register(fs)
	var root string
	fs.StringVar(&root, "root", "/devices", "path to devices root (usually /devices)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	c, err := cf.Mount()
	if err != nil {
		dief(stderr, "mount: %v", err)
		return 2
	}
	defer c.Unmount()

	// Trigger refresh (best-effort).
	_ = cli9p.WriteAll(c, path.Join(root, "discover"), []byte("refresh\n"), true)
	b, err := cli9p.ReadAll(c, path.Join(root, "discover"))
	if err != nil {
		dief(stderr, "read discover: %v", err)
		return 2
	}
	_, _ = stdout.Write(b)
	return 0
}

func cmdDevices(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("devices", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf cli9p.ConnFlags
	cf.Register(fs)
	var root string
	fs.StringVar(&root, "root", "/devices", "path to devices root (usually /devices)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, err := cf.Mount()
	if err != nil {
		dief(stderr, "mount: %v", err)
		return 2
	}
	defer c.Unmount()
	b, err := cli9p.ReadAll(c, path.Join(root, "discover"))
	if err != nil {
		dief(stderr, "read discover: %v", err)
		return 2
	}
	_, _ = stdout.Write(b)
	return 0
}

func cmdMetaStatus(stdout, stderr io.Writer, which string, args []string) int {
	fs := flag.NewFlagSet(which, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf cli9p.ConnFlags
	cf.Register(fs)
	var root, id string
	fs.StringVar(&root, "root", "/devices", "path to devices root (usually /devices)")
	fs.StringVar(&id, "id", "", "device id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if id == "" {
		dief(stderr, "missing -id")
		return 2
	}
	c, err := cf.Mount()
	if err != nil {
		dief(stderr, "mount: %v", err)
		return 2
	}
	defer c.Unmount()

	b, err := cli9p.ReadAll(c, path.Join(root, "by-id", id, which))
	if err != nil {
		dief(stderr, "read %s: %v", which, err)
		return 2
	}
	_, _ = stdout.Write(b)
	return 0
}

func cmdValue(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("value", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf cli9p.ConnFlags
	cf.Register(fs)
	var root, id, name string
	fs.StringVar(&root, "root", "/devices", "path to devices root (usually /devices)")
	fs.StringVar(&id, "id", "", "device id")
	fs.StringVar(&name, "name", "", "value name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if id == "" || name == "" {
		dief(stderr, "missing -id or -name")
		return 2
	}

	c, err := cf.Mount()
	if err != nil {
		dief(stderr, "mount: %v", err)
		return 2
	}
	defer c.Unmount()

	b, err := cli9p.ReadAll(c, path.Join(root, "by-id", id, "values", name, "value"))
	if err != nil {
		dief(stderr, "read value: %v", err)
		return 2
	}
	_, _ = stdout.Write(b)
	return 0
}

func cmdCall(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cf cli9p.ConnFlags
	cf.Register(fs)

	var root, id, fn string
	var payload, payloadFile string
	var stdin bool
	var stream bool

	fs.StringVar(&root, "root", "/devices", "path to devices root (usually /devices)")
	fs.StringVar(&id, "id", "", "device id")
	fs.StringVar(&fn, "fn", "", "function name")
	fs.StringVar(&payload, "payload", "", "payload literal (utf-8 text)")
	fs.StringVar(&payloadFile, "payload-file", "", "read payload bytes from file path")
	fs.BoolVar(&stdin, "stdin", false, "read payload bytes from stdin")
	fs.BoolVar(&stream, "stream", false, "use ctl stream instead of ctl call (if supported)")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if id == "" || fn == "" {
		dief(stderr, "missing -id or -fn")
		return 2
	}
	nsrc := 0
	if payload != "" {
		nsrc++
	}
	if payloadFile != "" {
		nsrc++
	}
	if stdin {
		nsrc++
	}
	if nsrc > 1 {
		dief(stderr, "choose only one of -payload, -payload-file, -stdin")
		return 2
	}

	var req []byte
	switch {
	case payload != "":
		req = []byte(payload)
	case payloadFile != "":
		b, err := os.ReadFile(payloadFile)
		if err != nil {
			dief(stderr, "read payload-file: %v", err)
			return 2
		}
		req = b
	case stdin:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			dief(stderr, "read stdin: %v", err)
			return 2
		}
		req = b
	default:
		req = nil
	}

	c, err := cf.Mount()
	if err != nil {
		dief(stderr, "mount: %v", err)
		return 2
	}
	defer c.Unmount()

	fnDir := path.Join(root, "by-id", id, "functions", fn)
	callID, err := allocateClone(c, path.Join(fnDir, "clone"))
	if err != nil {
		dief(stderr, "clone: %v", err)
		return 2
	}
	callDir := path.Join(fnDir, callID)
	dataPath := path.Join(callDir, "data")
	ctlPath := path.Join(callDir, "ctl")
	errPath := path.Join(callDir, "error")
	streamPath := path.Join(callDir, "stream")

	if len(req) != 0 {
		if err := cli9p.WriteAll(c, dataPath, req, true); err != nil {
			dief(stderr, "write request data: %v", err)
			return 2
		}
	}

	ctlCmd := "call\n"
	if stream {
		ctlCmd = "stream\n"
	}
	if err := cli9p.WriteAll(c, ctlPath, []byte(ctlCmd), true); err != nil {
		// Try to surface richer error.
		if eb, e2 := cli9p.ReadAll(c, errPath); e2 == nil {
			msg := strings.TrimSpace(string(eb))
			if msg != "" {
				dief(stderr, "invoke failed: %s", msg)
				return 2
			}
		}
		dief(stderr, "invoke failed: %v", err)
		return 2
	}

	outPath := dataPath
	if stream {
		outPath = streamPath
	}
	b, err := cli9p.ReadAll(c, outPath)
	if err != nil {
		dief(stderr, "read response: %v", err)
		return 2
	}
	_, _ = stdout.Write(b)
	return 0
}

func allocateClone(c *clnt.Clnt, clonePath string) (string, error) {
	b, err := cli9p.ReadAll(c, clonePath)
	if err != nil {
		return "", err
	}
	id := cli9p.TrimLine(b)
	if id == "" {
		return "", fmt.Errorf("clone returned empty id")
	}
	return id, nil
}

func run(stdout, stderr io.Writer, argv []string) int {
	if len(argv) < 1 {
		usage()
		return 2
	}
	switch argv[0] {
	case "discover":
		return cmdDiscover(stdout, stderr, argv[1:])
	case "devices":
		return cmdDevices(stdout, stderr, argv[1:])
	case "meta":
		return cmdMetaStatus(stdout, stderr, "meta", argv[1:])
	case "status":
		return cmdMetaStatus(stdout, stderr, "status", argv[1:])
	case "value":
		return cmdValue(stdout, stderr, argv[1:])
	case "call":
		return cmdCall(stdout, stderr, argv[1:])
	default:
		usage()
		return 2
	}
}

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Args[1:]))
}

