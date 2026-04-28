package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lionkov/go9p/cmd/internal/kfs"
)

func dief(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage (kernel-mounted paths):\n")
	fmt.Fprintf(os.Stderr, "  %s -root <mount>/devices discover\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s -root <mount>/devices devices\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s -root <mount>/devices meta   -id <device-id>\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s -root <mount>/devices status -id <device-id>\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s -root <mount>/devices value  -id <device-id> -name <value-name>\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s -root <mount>/devices call   -id <device-id> -fn <function> [-payload <text>|-payload-file <path>|-stdin] [-stream]\n", os.Args[0])
}

func cmdDiscover(root string, refresh bool) {
	disc := filepath.Join(root, "discover")
	if refresh {
		_ = kfs.WriteAll(disc, []byte("refresh\n"))
	}
	b, err := kfs.ReadAll(disc)
	if err != nil {
		dief("read discover: %v", err)
	}
	_, _ = os.Stdout.Write(b)
}

func cmdMetaStatus(root, id, which string) {
	if id == "" {
		dief("missing -id")
	}
	p := filepath.Join(root, "by-id", id, which)
	b, err := kfs.ReadAll(p)
	if err != nil {
		dief("read %s: %v", which, err)
	}
	_, _ = os.Stdout.Write(b)
}

func cmdValue(root, id, name string) {
	if id == "" || name == "" {
		dief("missing -id or -name")
	}
	p := filepath.Join(root, "by-id", id, "values", name, "value")
	b, err := kfs.ReadAll(p)
	if err != nil {
		dief("read value: %v", err)
	}
	_, _ = os.Stdout.Write(b)
}

func cmdCall(root, id, fn string, req []byte, stream bool) {
	if id == "" || fn == "" {
		dief("missing -id or -fn")
	}
	fnDir := filepath.Join(root, "by-id", id, "functions", fn)
	callID, err := kfs.CloneID(filepath.Join(fnDir, "clone"))
	if err != nil {
		dief("clone: %v", err)
	}
	callDir := filepath.Join(fnDir, callID)
	dataPath := filepath.Join(callDir, "data")
	ctlPath := filepath.Join(callDir, "ctl")
	errPath := filepath.Join(callDir, "error")
	streamPath := filepath.Join(callDir, "stream")

	// Reset per-call buffers so writes don't rely on truncate semantics.
	_ = kfs.WriteAll(ctlPath, []byte("reset\n"))

	if len(req) != 0 {
		if err := kfs.WriteAll(dataPath, req); err != nil {
			dief("write request data: %v", err)
		}
	}

	ctlCmd := []byte("call\n")
	if stream {
		ctlCmd = []byte("stream\n")
	}
	if err := kfs.WriteAll(ctlPath, ctlCmd); err != nil {
		if eb, e2 := kfs.ReadAll(errPath); e2 == nil {
			msg := strings.TrimSpace(string(eb))
			if msg != "" {
				dief("invoke failed: %s", msg)
			}
		}
		dief("invoke failed: %v", err)
	}

	outPath := dataPath
	if stream {
		outPath = streamPath
	}
	b, err := kfs.ReadAll(outPath)
	if err != nil {
		dief("read response: %v", err)
	}
	_, _ = os.Stdout.Write(b)
}

func main() {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	var root string
	root = strings.TrimSpace(os.Getenv("GO9P_DEVICECONNECT_ROOT"))
	if root == "" {
		root = "/mnt/go9p/deviceconnect/devices"
	}
	fs.StringVar(&root, "root", root, "path to mounted deviceconnect root (e.g. /mnt/go9p/deviceconnect/devices)")
	_ = fs.Parse(os.Args[1:])
	if root == "" || fs.NArg() < 1 {
		usage()
		os.Exit(2)
	}

	switch fs.Arg(0) {
	case "discover":
		cmdDiscover(root, true)
	case "devices":
		cmdDiscover(root, false)
	case "meta":
		ff := flag.NewFlagSet("meta", flag.ExitOnError)
		ff.SetOutput(os.Stderr)
		var id string
		ff.StringVar(&id, "id", "", "device id")
		_ = ff.Parse(fs.Args()[1:])
		cmdMetaStatus(root, id, "meta")
	case "status":
		ff := flag.NewFlagSet("status", flag.ExitOnError)
		ff.SetOutput(os.Stderr)
		var id string
		ff.StringVar(&id, "id", "", "device id")
		_ = ff.Parse(fs.Args()[1:])
		cmdMetaStatus(root, id, "status")
	case "value":
		ff := flag.NewFlagSet("value", flag.ExitOnError)
		ff.SetOutput(os.Stderr)
		var id, name string
		ff.StringVar(&id, "id", "", "device id")
		ff.StringVar(&name, "name", "", "value name")
		_ = ff.Parse(fs.Args()[1:])
		cmdValue(root, id, name)
	case "call":
		ff := flag.NewFlagSet("call", flag.ExitOnError)
		ff.SetOutput(os.Stderr)
		var id, fn, payload, payloadFile string
		var stdin bool
		var stream bool
		ff.StringVar(&id, "id", "", "device id")
		ff.StringVar(&fn, "fn", "", "function name")
		ff.StringVar(&payload, "payload", "", "payload literal (utf-8 text)")
		ff.StringVar(&payloadFile, "payload-file", "", "read payload bytes from file path")
		ff.BoolVar(&stdin, "stdin", false, "read payload bytes from stdin")
		ff.BoolVar(&stream, "stream", false, "use ctl stream instead of ctl call (if supported)")
		_ = ff.Parse(fs.Args()[1:])

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
			dief("choose only one of -payload, -payload-file, -stdin")
		}

		var req []byte
		switch {
		case payload != "":
			req = []byte(payload)
		case payloadFile != "":
			b, err := os.ReadFile(payloadFile)
			if err != nil {
				dief("read payload-file: %v", err)
			}
			req = b
		case stdin:
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				dief("read stdin: %v", err)
			}
			req = b
		default:
			req = nil
		}
		cmdCall(root, id, fn, req, stream)
	default:
		usage()
		os.Exit(2)
	}
}

