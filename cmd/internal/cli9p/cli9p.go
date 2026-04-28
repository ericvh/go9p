package cli9p

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/lionkov/go9p/p"
	"github.com/lionkov/go9p/p/clnt"
)

type ConnFlags struct {
	Addr       string
	Network    string
	AttachName string
	Msize      uint
	Dotu       bool
	Debug      int
}

func (cf *ConnFlags) Register(fs *flag.FlagSet) {
	fs.StringVar(&cf.Network, "net", "tcp", "network (tcp, unix)")
	fs.StringVar(&cf.Addr, "addr", "127.0.0.1:5640", "server address (host:port for tcp; path for unix)")
	fs.StringVar(&cf.AttachName, "aname", "", "attach name (usually empty)")
	fs.UintVar(&cf.Msize, "msize", 8192, "9p msize")
	fs.BoolVar(&cf.Dotu, "dotu", true, "enable 9P2000.u dialect when available")
	fs.IntVar(&cf.Debug, "d", 0, "9p client debug flags (see p/clnt)")
}

func (cf *ConnFlags) Mount() (*clnt.Clnt, error) {
	user := p.OsUsers.Uid2User(os.Geteuid())
	clnt.DefaultDebuglevel = cf.Debug
	if cf.Network == "unix" {
		conn, err := net.Dial("unix", cf.Addr)
		if err != nil {
			return nil, err
		}
		c, err := clnt.Connect(conn, uint32(cf.Msize), cf.Dotu)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if _, err := c.Attach(nil, user, cf.AttachName); err != nil {
			c.Unmount()
			return nil, err
		}
		return c, nil
	}
	return clnt.Mount(cf.Network, cf.Addr, cf.AttachName, uint32(cf.Msize), user)
}

func ReadAll(c *clnt.Clnt, path string) ([]byte, error) {
	f, err := c.FOpen(path, p.OREAD)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []byte
	buf := make([]byte, 32*1024)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if n == 0 {
			if rerr != nil && !errors.Is(rerr, io.EOF) {
				return nil, rerr
			}
			return out, nil
		}
	}
}

func WriteAll(c *clnt.Clnt, path string, data []byte, trunc bool) error {
	mode := uint8(p.OWRITE)
	if trunc {
		mode |= p.OTRUNC
	}
	f, err := c.FOpen(path, mode)
	if err != nil {
		f, err = c.FCreate(path, 0o666, p.OWRITE)
		if err != nil {
			return err
		}
	}
	defer f.Close()

	for len(data) > 0 {
		n, werr := f.Write(data)
		if werr != nil {
			return werr
		}
		data = data[n:]
	}
	return nil
}

func TrimLine(b []byte) string {
	return strings.TrimSpace(string(b))
}

func UsageSubcommands(bin string, lines ...string) func() {
	return func() {
		w := flag.CommandLine.Output()
		fmt.Fprintf(w, "Usage:\n  %s <command> [args]\n\nCommands:\n", bin)
		for _, ln := range lines {
			fmt.Fprintf(w, "  %s\n", ln)
		}
	}
}

func TrapInterrupt(cancel func()) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
		<-ch
		os.Exit(2)
	}()
}

