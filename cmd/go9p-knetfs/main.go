package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"os/signal"
	"strings"
	"sync"

	"github.com/lionkov/go9p/cmd/internal/kfs"
)

func dief(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage (kernel-mounted paths):\n")
	fmt.Fprintf(os.Stderr, "  %s -netroot <mount>/net tcp-alloc\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s -netroot <mount>/net tcp-connect [-id <id>] <host!port|host:port|\"host port\">\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s -netroot <mount>/net tcp-dial <host!port|host:port|\"host port\">\n", os.Args[0])
}

func alloc(netroot string) (string, error) {
	return kfs.CloneID(filepath.Join(netroot, "tcp", "clone"))
}

func connect(netroot, id, target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("empty target")
	}
	if id == "" {
		var err error
		id, err = alloc(netroot)
		if err != nil {
			return "", err
		}
	}
	ctl := filepath.Join(netroot, "tcp", id, "ctl")
	if err := kfs.WriteAll(ctl, []byte("connect "+target+"\n")); err != nil {
		return "", err
	}
	return id, nil
}

func dial(netroot, target string) {
	id, err := connect(netroot, "", target)
	if err != nil {
		dief("connect: %v", err)
	}
	ctl := filepath.Join(netroot, "tcp", id, "ctl")
	data := filepath.Join(netroot, "tcp", id, "data")

	rf, err := os.OpenFile(data, os.O_RDONLY, 0)
	if err != nil {
		dief("open data for read: %v", err)
	}
	wf, err := os.OpenFile(data, os.O_WRONLY, 0)
	if err != nil {
		_ = rf.Close()
		dief("open data for write: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	kfsTrapInterrupt(cancel)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(os.Stdout, rf)
		cancel()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(wf, os.Stdin)
		cancel()
	}()

	<-ctx.Done()
	wg.Wait()
	_ = kfs.WriteAll(ctl, []byte("close\n"))
}

func kfsTrapInterrupt(cancel func()) {
	// Local copy (avoid importing signal/syscall into internal helper for now).
	ch := make(chan os.Signal, 2)
	// os.Interrupt is enough for these CLIs; SIGTERM support can be added later.
	signalNotify(ch)
	go func() {
		<-ch
		cancel()
		<-ch
		os.Exit(2)
	}()
}

func signalNotify(ch chan<- os.Signal) {
	// Defer to standard library when available (inlined to keep this file self-contained).
	// This is implemented in a separate function so it can be trivially swapped if needed.
	signal.Notify(ch, os.Interrupt)
}

func main() {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	var netroot string
	fs.StringVar(&netroot, "netroot", "", "path to mounted /net (e.g. /mnt/9p/net)")
	_ = fs.Parse(os.Args[1:])
	if netroot == "" || fs.NArg() < 1 {
		usage()
		os.Exit(2)
	}

	switch fs.Arg(0) {
	case "tcp-alloc":
		id, err := alloc(netroot)
		if err != nil {
			dief("alloc: %v", err)
		}
		fmt.Println(id)
	case "tcp-connect":
		ff := flag.NewFlagSet("tcp-connect", flag.ExitOnError)
		ff.SetOutput(os.Stderr)
		var id string
		ff.StringVar(&id, "id", "", "conversation id (optional; allocates if empty)")
		_ = ff.Parse(fs.Args()[1:])
		if ff.NArg() != 1 {
			dief("tcp-connect expects one target argument")
		}
		got, err := connect(netroot, id, ff.Arg(0))
		if err != nil {
			dief("connect: %v", err)
		}
		fmt.Println(got)
	case "tcp-dial":
		if len(fs.Args()) != 2 {
			dief("tcp-dial expects one target argument")
		}
		dial(netroot, fs.Arg(1))
	default:
		usage()
		os.Exit(2)
	}
}

