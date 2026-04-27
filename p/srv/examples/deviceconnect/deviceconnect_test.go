package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/lionkov/go9p/p"
	"github.com/lionkov/go9p/p/clnt"
	"github.com/lionkov/go9p/p/srv"
)

type fakeBackend struct {
	devices []Device
	invoke  func(deviceID, fn string, payload []byte) ([]byte, error)
}

func (b *fakeBackend) ListDevices(ctx context.Context) ([]Device, error) { return b.devices, nil }
func (b *fakeBackend) Invoke(ctx context.Context, deviceID string, fn string, payload []byte) ([]byte, error) {
	if b.invoke != nil {
		return b.invoke(deviceID, fn, payload)
	}
	return []byte("ok\n"), nil
}

func startDeviceConnectServer(t *testing.T, backend Backend) (addr string, stop func()) {
	t.Helper()

	fs, err := buildDeviceConnectFS(backend)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := srv.NewFileSrv(fs.root)
	s.Dotu = true
	s.Start(s)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.StartListener(ln) }()

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

func mountClient(t *testing.T, addr string) (*clnt.Clnt, func()) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := clnt.NewClnt(conn, 8192, true)
	user := p.OsUsers.Uid2User(os.Geteuid())
	if _, err := c.Attach(nil, user, "/"); err != nil {
		_ = conn.Close()
		t.Fatalf("attach: %v", err)
	}
	return c, func() {
		c.Unmount()
		_ = conn.Close()
	}
}

func TestDeviceConnect_DiscoverAndHierarchy(t *testing.T) {
	backend := &fakeBackend{
		devices: []Device{
			{
				ID:     "sensor-001",
				Type:   "sensor",
				Meta:   "id=sensor-001 type=sensor",
				Status: "ok",
				Functions: []Function{
					{Name: "get_reading", About: "Return reading.", Schema: "write: none"},
				},
			},
		},
	}
	addr, stop := startDeviceConnectServer(t, backend)
	defer stop()

	c, cleanup := mountClient(t, addr)
	defer cleanup()

	f, err := c.FOpen("/devices/discover", p.OREAD)
	if err != nil {
		t.Fatalf("open discover: %v", err)
	}
	defer f.Close()

	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil {
		t.Fatalf("read discover: %v", err)
	}
	if strings.TrimSpace(line) != "sensor-001" {
		t.Fatalf("discover line=%q", line)
	}

	meta, err := c.FOpen("/devices/by-id/sensor-001/meta", p.OREAD)
	if err != nil {
		t.Fatalf("open meta: %v", err)
	}
	defer meta.Close()
	b, err := io.ReadAll(meta)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if !strings.Contains(string(b), "sensor-001") {
		t.Fatalf("meta=%q", string(b))
	}

	about, err := c.FOpen("/devices/by-id/sensor-001/functions/get_reading/about", p.OREAD)
	if err != nil {
		t.Fatalf("open about: %v", err)
	}
	defer about.Close()
	ab, err := io.ReadAll(about)
	if err != nil {
		t.Fatalf("read about: %v", err)
	}
	if !strings.Contains(string(ab), "Return reading") {
		t.Fatalf("about=%q", string(ab))
	}
}

func TestDeviceConnect_InvokeAndResult(t *testing.T) {
	backend := &fakeBackend{
		devices: []Device{
			{
				ID:     "robot-001",
				Type:   "robot",
				Meta:   "id=robot-001 type=robot",
				Status: "ok",
				Functions: []Function{
					{Name: "echo", About: "Echo.", Schema: "bytes"},
				},
			},
		},
		invoke: func(deviceID, fn string, payload []byte) ([]byte, error) {
			if deviceID != "robot-001" || fn != "echo" {
				return nil, errors.New("unexpected invoke")
			}
			return append([]byte(nil), payload...), nil
		},
	}
	addr, stop := startDeviceConnectServer(t, backend)
	defer stop()

	c, cleanup := mountClient(t, addr)
	defer cleanup()

	inv, err := c.FOpen("/devices/by-id/robot-001/functions/echo/invoke", p.OWRITE)
	if err != nil {
		t.Fatalf("open invoke: %v", err)
	}
	if _, err := inv.Write([]byte("hi\n")); err != nil {
		t.Fatalf("write invoke: %v", err)
	}
	_ = inv.Close()

	res, err := c.FOpen("/devices/by-id/robot-001/functions/echo/result", p.OREAD)
	if err != nil {
		t.Fatalf("open result: %v", err)
	}
	defer res.Close()
	got, err := io.ReadAll(res)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "hi\n" {
		t.Fatalf("result=%q", string(got))
	}
}
