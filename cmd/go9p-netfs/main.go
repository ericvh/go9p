package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/lionkov/go9p/cmd/internal/cli9p"
	"github.com/lionkov/go9p/p"
	"github.com/lionkov/go9p/p/clnt"
)

func dief(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
}

func cmdTCPDial(args []string) {
	fs := flag.NewFlagSet("tcp-dial", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var cf cli9p.ConnFlags
	// Canonical defaults (override via GO9P_NETFS_ADDR or GO9P_ADDR).
	cf.Addr = strings.TrimSpace(os.Getenv("GO9P_NETFS_ADDR"))
	if cf.Addr == "" {
		cf.Addr = strings.TrimSpace(os.Getenv("GO9P_ADDR"))
	}
	if cf.Addr == "" {
		cf.Addr = "127.0.0.1:5641"
	}
	cf.Register(fs)

	var netRoot string
	netRoot = strings.TrimSpace(os.Getenv("GO9P_NETROOT"))
	if netRoot == "" {
		netRoot = "/net"
	}
	fs.StringVar(&netRoot, "netroot", netRoot, "path to net root (usually /net)")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		dief("tcp-dial expects one argument: host!port | host:port | \"host port\"")
	}
	target := fs.Arg(0)
	// Allow "host port" by passing as a single arg via shell quoting.
	target = strings.TrimSpace(target)
	if target == "" {
		dief("empty target")
	}

	c, err := cf.Mount()
	if err != nil {
		dief("mount: %v", err)
	}
	defer c.Unmount()

	convID, err := allocateClone(c, netRoot+"/tcp/clone")
	if err != nil {
		dief("alloc conversation: %v", err)
	}
	ctl := fmt.Sprintf("%s/tcp/%s/ctl", netRoot, convID)
	data := fmt.Sprintf("%s/tcp/%s/data", netRoot, convID)

	// Keep ctl open until we're done with this conversation. The server GC's the
	// session directory when the last ctl reference is closed.
	ctlf, err := c.FOpen(ctl, p.OWRITE)
	if err != nil {
		dief("open ctl: %v", err)
	}
	defer ctlf.Close()

	if _, err := ctlf.Write([]byte("connect " + target + "\n")); err != nil {
		dief("connect: %v", err)
	}

	rf, err := c.FOpen(data, p.OREAD)
	if err != nil {
		dief("open data for read: %v", err)
	}
	defer rf.Close()

	wf, err := c.FOpen(data, p.OWRITE)
	if err != nil {
		dief("open data for write: %v", err)
	}
	defer wf.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cli9p.TrapInterrupt(cancel)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(os.Stdout, &fileReader{f: rf})
		cancel()
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(&fileWriter{f: wf}, os.Stdin)
		cancel()
	}()

	<-ctx.Done()
	wg.Wait()
	_, _ = ctlf.Write([]byte("close\n"))
}

type fileReader struct{ f *clnt.File }

func (r *fileReader) Read(p []byte) (int, error) { return r.f.Read(p) }

type fileWriter struct{ f *clnt.File }

func (w *fileWriter) Write(p []byte) (int, error) { return w.f.Write(p) }

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

func main() {
	flag.Usage = cli9p.UsageSubcommands(os.Args[0],
		"netfs tcp-dial   Dial /net/tcp using clone+ctl+data (nc-like)",
	)
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	switch flag.Arg(0) {
	case "tcp-dial":
		cmdTCPDial(flag.Args()[1:])
	default:
		flag.Usage()
		os.Exit(2)
	}
}

