package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lionkov/go9p/p"
	"github.com/lionkov/go9p/p/clnt"
	"github.com/lionkov/go9p/p/srv"
)

func startNetFSServer(t *testing.T) (addr string, stop func()) {
	t.Helper()

	nfs, err := buildNetFS()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	nfs.srv = srv.NewFileSrv(nfs.root)
	nfs.srv.Dotu = true
	nfs.srv.Start(nfs.srv)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- nfs.srv.StartListener(ln) }()

	return ln.Addr().String(), func() {
		_ = ln.Close()
		if err := <-errCh; err != nil &&
			!errors.Is(err, net.ErrClosed) &&
			!errors.Is(err, os.ErrClosed) &&
			!strings.Contains(err.Error(), "use of closed network connection") {
			t.Fatalf("server: %v", err)
		}
	}
}

func startTCPEcho(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(conn, conn)
			}(c)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func TestNetFS_TCPConnectAndEcho(t *testing.T) {
	srvAddr, stop := startNetFSServer(t)
	defer stop()

	echoAddr, stopEcho := startTCPEcho(t)
	defer stopEcho()

	conn, err := net.Dial("tcp", srvAddr)
	if err != nil {
		t.Fatalf("dial 9p: %v", err)
	}
	defer conn.Close()

	c := clnt.NewClnt(conn, 8192, true)
	defer c.Unmount()

	user := p.OsUsers.Uid2User(os.Geteuid())
	_, err = c.Attach(nil, user, "/")
	if err != nil {
		t.Fatalf("attach: %v", err)
	}

	clone, err := c.FOpen("/net/tcp/clone", p.OREAD)
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	defer clone.Close()

	r := bufio.NewReader(clone)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read clone: %v", err)
	}
	id := strings.TrimSpace(line)
	if id == "" {
		t.Fatalf("empty clone id")
	}

	ctl, err := c.FOpen("/net/tcp/"+id+"/ctl", p.OWRITE)
	if err != nil {
		t.Fatalf("open ctl: %v", err)
	}
	defer ctl.Close()

	if _, err := ctl.Write([]byte("connect " + echoAddr + "\n")); err != nil {
		t.Fatalf("ctl connect: %v", err)
	}

	data, err := c.FOpen("/net/tcp/"+id+"/data", p.ORDWR)
	if err != nil {
		t.Fatalf("open data: %v", err)
	}
	defer data.Close()

	want := []byte("hello-netfs\n")
	if _, err := data.Write(want); err != nil {
		t.Fatalf("write data: %v", err)
	}

	got := make([]byte, len(want))
	if _, err := io.ReadFull(data, got); err != nil {
		t.Fatalf("read data: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %q want %q", string(got), string(want))
	}
}

