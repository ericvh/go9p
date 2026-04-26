// devnet is a go9p synthetic filesystem that models a small subset of Plan 9's /net.
//
// It focuses on the TCP "conversation" pattern:
//   - /net/tcp/clone allocates a new conversation directory N
//   - /net/tcp/N/ctl accepts "connect host port" (or "connect host!port") and "close"
//   - /net/tcp/N/data streams bytes to/from the underlying TCP connection
//   - /net/tcp/N/{local,remote,status} expose basic metadata
//
// This is intentionally a minimal, educational slice of devnet rather than a full
// kernel-integrated network stack.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lionkov/go9p/p"
	"github.com/lionkov/go9p/p/srv"
)

var (
	addr  = flag.String("addr", ":5640", "network address")
	debug = flag.Bool("d", false, "print debug messages")
)

type Devnet struct {
	srv *srv.Fsrv

	mu      sync.Mutex
	nextTCP int

	root *srv.File
	net  *srv.File
	tcp  *srv.File
}

type TCPClone struct {
	srv.File
	dn *Devnet
}

type TCPConvDir struct {
	srv.File
	conv *TCPConv
}

type TCPConv struct {
	id int

	mu       sync.Mutex
	conn     net.Conn
	state    string
	lastErr  string
	created  time.Time
	peerAddr string
}

type TCPCTL struct {
	srv.File
	conv *TCPConv
}

type TCPData struct {
	srv.File
	conv *TCPConv
}

type TCPStatus struct {
	srv.File
	conv *TCPConv
}

type TCPStringFile struct {
	srv.File
	conv *TCPConv
	kind string // "local" | "remote"
}

func (c *TCPConv) snapshot() (state, local, remote, lastErr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state = c.state
	lastErr = c.lastErr
	if c.conn != nil {
		local = c.conn.LocalAddr().String()
		remote = c.conn.RemoteAddr().String()
	}
	if remote == "" {
		remote = c.peerAddr
	}
	return state, local, remote, lastErr
}

func (c *TCPConv) connect(hostport string) error {
	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		return fmt.Errorf("already connected")
	}
	c.state = "connecting"
	c.peerAddr = hostport
	c.lastErr = ""
	c.mu.Unlock()

	conn, err := net.Dial("tcp", hostport)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.state = "error"
		c.lastErr = err.Error()
		return err
	}
	c.conn = conn
	c.state = "connected"
	c.peerAddr = conn.RemoteAddr().String()
	return nil
}

func (c *TCPConv) close() error {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.state = "closed"
	c.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func parseConnectArg(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("missing address")
	}
	// Accept "host!port" as Plan 9-ish spelling.
	if strings.Count(s, "!") == 1 && !strings.Contains(s, " ") {
		parts := strings.SplitN(s, "!", 2)
		if parts[0] == "" || parts[1] == "" {
			return "", fmt.Errorf("invalid address %q", s)
		}
		return net.JoinHostPort(parts[0], parts[1]), nil
	}
	// Accept "host:port" as a single token.
	if strings.Count(s, ":") >= 1 && !strings.Contains(s, " ") {
		_, _, err := net.SplitHostPort(s)
		if err != nil {
			return "", err
		}
		return s, nil
	}
	// Accept "host port" (two tokens).
	fields := strings.Fields(s)
	if len(fields) == 2 {
		return net.JoinHostPort(fields[0], fields[1]), nil
	}
	return "", fmt.Errorf("expected host!port, host:port, or host port; got %q", s)
}

func (f *TCPClone) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	// A single read from clone allocates a new conversation and returns its id.
	if offset > 0 {
		return 0, nil
	}

	f.dn.mu.Lock()
	f.dn.nextTCP++
	id := f.dn.nextTCP
	f.dn.mu.Unlock()

	conv := &TCPConv{id: id, state: "new", created: time.Now()}

	// Create /net/tcp/<id> directory.
	dir := new(TCPConvDir)
	dir.conv = conv
	if err := dir.Add(f.dn.tcp, strconv.Itoa(id), p.OsUsers.Uid2User(os.Geteuid()), nil, p.DMDIR|0555, dir); err != nil {
		return 0, err
	}

	// Populate /net/tcp/<id>/{ctl,data,status,local,remote}.
	ctl := new(TCPCTL)
	ctl.conv = conv
	if err := ctl.Add(&dir.File, "ctl", p.OsUsers.Uid2User(os.Geteuid()), nil, 0o666, ctl); err != nil {
		return 0, err
	}
	data := new(TCPData)
	data.conv = conv
	if err := data.Add(&dir.File, "data", p.OsUsers.Uid2User(os.Geteuid()), nil, 0o666, data); err != nil {
		return 0, err
	}
	st := new(TCPStatus)
	st.conv = conv
	if err := st.Add(&dir.File, "status", p.OsUsers.Uid2User(os.Geteuid()), nil, 0o444, st); err != nil {
		return 0, err
	}
	local := new(TCPStringFile)
	local.conv = conv
	local.kind = "local"
	if err := local.Add(&dir.File, "local", p.OsUsers.Uid2User(os.Geteuid()), nil, 0o444, local); err != nil {
		return 0, err
	}
	remote := new(TCPStringFile)
	remote.conv = conv
	remote.kind = "remote"
	if err := remote.Add(&dir.File, "remote", p.OsUsers.Uid2User(os.Geteuid()), nil, 0o444, remote); err != nil {
		return 0, err
	}

	out := []byte(strconv.Itoa(id) + "\n")
	if len(out) > len(buf) {
		out = out[:len(buf)]
	}
	copy(buf, out)
	return len(out), nil
}

func (f *TCPCTL) Write(fid *srv.FFid, data []byte, offset uint64) (int, error) {
	// ctl is command-oriented; ignore offset and treat each write as a command.
	cmd := strings.TrimSpace(string(data))
	if cmd == "" {
		return len(data), nil
	}
	fields := strings.Fields(cmd)
	switch fields[0] {
	case "connect":
		arg := strings.TrimSpace(strings.TrimPrefix(cmd, "connect"))
		hp, err := parseConnectArg(arg)
		if err != nil {
			return 0, err
		}
		if err := f.conv.connect(hp); err != nil {
			return 0, err
		}
		return len(data), nil
	case "close":
		_ = f.conv.close()
		return len(data), nil
	default:
		return 0, fmt.Errorf("unknown ctl command %q", fields[0])
	}
}

func (f *TCPData) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	// Stream-oriented: ignore offset and read directly from the connection.
	f.conv.mu.Lock()
	conn := f.conv.conn
	f.conv.mu.Unlock()
	if conn == nil {
		return 0, &p.Error{Err: "not connected", Errornum: p.EIO}
	}
	n, err := conn.Read(buf)
	if err != nil {
		if err == io.EOF {
			return 0, nil
		}
		return n, err
	}
	return n, nil
}

func (f *TCPData) Write(fid *srv.FFid, data []byte, offset uint64) (int, error) {
	// Stream-oriented: ignore offset and write directly to the connection.
	f.conv.mu.Lock()
	conn := f.conv.conn
	f.conv.mu.Unlock()
	if conn == nil {
		return 0, &p.Error{Err: "not connected", Errornum: p.EIO}
	}
	return conn.Write(data)
}

func (f *TCPStatus) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	state, local, remote, lastErr := f.conv.snapshot()
	line := fmt.Sprintf("id=%d state=%s local=%s remote=%s err=%s\n", f.conv.id, state, local, remote, lastErr)
	b := []byte(line)
	if offset >= uint64(len(b)) {
		return 0, nil
	}
	b = b[offset:]
	if len(b) > len(buf) {
		b = b[:len(buf)]
	}
	copy(buf, b)
	return len(b), nil
}

func (f *TCPStringFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	_, local, remote, _ := f.conv.snapshot()
	val := ""
	switch f.kind {
	case "local":
		val = local
	case "remote":
		val = remote
	default:
		val = ""
	}
	if val == "" {
		val = "-"
	}
	val += "\n"
	b := []byte(val)
	if offset >= uint64(len(b)) {
		return 0, nil
	}
	b = b[offset:]
	if len(b) > len(buf) {
		b = b[:len(buf)]
	}
	copy(buf, b)
	return len(b), nil
}

func buildDevnet() (*Devnet, error) {
	dn := &Devnet{}
	user := p.OsUsers.Uid2User(os.Geteuid())

	dn.root = new(srv.File)
	if err := dn.root.Add(nil, "/", user, nil, p.DMDIR|0555, nil); err != nil {
		return nil, err
	}

	dn.net = new(srv.File)
	if err := dn.net.Add(dn.root, "net", user, nil, p.DMDIR|0555, nil); err != nil {
		return nil, err
	}

	dn.tcp = new(srv.File)
	if err := dn.tcp.Add(dn.net, "tcp", user, nil, p.DMDIR|0555, nil); err != nil {
		return nil, err
	}

	clone := new(TCPClone)
	clone.dn = dn
	if err := clone.Add(dn.tcp, "clone", user, nil, 0o444, clone); err != nil {
		return nil, err
	}

	return dn, nil
}

func main() {
	flag.Parse()

	dn, err := buildDevnet()
	if err != nil {
		log.Fatalf("build devnet: %v", err)
	}

	dn.srv = srv.NewFileSrv(dn.root)
	dn.srv.Dotu = true
	if *debug {
		dn.srv.Debuglevel = 1
	}
	dn.srv.Start(dn.srv)

	if err := dn.srv.StartNetListener("tcp", *addr); err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}
}

