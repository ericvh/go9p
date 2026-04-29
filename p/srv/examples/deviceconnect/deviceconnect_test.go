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
	"time"

	"github.com/lionkov/go9p/p"
	"github.com/lionkov/go9p/p/clnt"
	"github.com/lionkov/go9p/p/srv"
)

type fakeBackend struct {
	devices []Device
	invoke  func(deviceID, fn string, payload []byte) ([]byte, error)
	stream  func(deviceID, fn string, payload []byte) (<-chan []byte, error)
	events  map[string]chan Event
	values  map[string]map[string][]byte
	vEvents map[string]map[string]chan Event
}

func (b *fakeBackend) ListDevices(ctx context.Context) ([]Device, error) { return b.devices, nil }
func (b *fakeBackend) ReadValue(ctx context.Context, deviceID string, name string) ([]byte, error) {
	if b.values == nil {
		return nil, errors.New("values not supported")
	}
	m, ok := b.values[deviceID]
	if !ok {
		return nil, errors.New("unknown device")
	}
	v, ok := m[name]
	if !ok {
		return nil, errors.New("unknown value")
	}
	return append([]byte(nil), v...), nil
}
func (b *fakeBackend) Invoke(ctx context.Context, deviceID string, fn string, payload []byte) ([]byte, error) {
	if b.invoke != nil {
		return b.invoke(deviceID, fn, payload)
	}
	return []byte("ok\n"), nil
}
func (b *fakeBackend) InvokeStream(ctx context.Context, deviceID string, fn string, payload []byte) (<-chan []byte, error) {
	if b.stream != nil {
		return b.stream(deviceID, fn, payload)
	}
	return nil, nil
}
func (b *fakeBackend) SubscribeDeviceEvents(ctx context.Context, deviceID string) (<-chan Event, error) {
	if b.events == nil {
		return nil, nil
	}
	ch, ok := b.events[deviceID]
	if !ok {
		return nil, nil
	}
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				out <- ev
			}
		}
	}()
	return out, nil
}
func (b *fakeBackend) SubscribeValueEvents(ctx context.Context, deviceID string, valueName string) (<-chan Event, error) {
	if b.vEvents == nil {
		return nil, nil
	}
	dm, ok := b.vEvents[deviceID]
	if !ok {
		return nil, nil
	}
	ch, ok := dm[valueName]
	if !ok {
		return nil, nil
	}
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				out <- ev
			}
		}
	}()
	return out, nil
}

func startDeviceConnectServer(t *testing.T, backend Backend) (addr string, stop func()) {
	t.Helper()

	fs, err := buildDeviceConnectFS(backend)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := srv.NewFileSrv(fs.root)
	s.Dotu = true
	ds := &dcSrv{Fsrv: s, fs: fs}
	ds.Start(ds)

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
				Values: []Value{
					{Name: "temp", About: "Temperature", Unit: "C"},
				},
				Functions: []Function{
					{Name: "get_reading", About: "Return reading.", Schema: "write: none"},
				},
			},
		},
		values: map[string]map[string][]byte{
			"sensor-001": {"temp": []byte("22.5")},
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

	vf, err := c.FOpen("/devices/by-id/sensor-001/values/temp/value", p.OREAD)
	if err != nil {
		t.Fatalf("open value: %v", err)
	}
	defer vf.Close()
	vb, err := io.ReadAll(vf)
	if err != nil {
		t.Fatalf("read value: %v", err)
	}
	if strings.TrimSpace(string(vb)) != "22.5" {
		t.Fatalf("value=%q", string(vb))
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

	cl, err := c.FOpen("/devices/by-id/robot-001/functions/echo/clone", p.OREAD)
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	idb, err := io.ReadAll(cl)
	_ = cl.Close()
	if err != nil {
		t.Fatalf("read clone: %v", err)
	}
	id := strings.TrimSpace(string(idb))
	if id == "" {
		t.Fatalf("empty clone id")
	}

	dataf, err := c.FOpen("/devices/by-id/robot-001/functions/echo/"+id+"/data", p.ORDWR)
	if err != nil {
		t.Fatalf("open data: %v", err)
	}
	if _, err := dataf.Write([]byte("hi\n")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	_ = dataf.Close()

	ctl, err := c.FOpen("/devices/by-id/robot-001/functions/echo/"+id+"/ctl", p.OWRITE)
	if err != nil {
		t.Fatalf("open ctl: %v", err)
	}
	if _, err := ctl.Write([]byte("call\n")); err != nil {
		t.Fatalf("ctl call: %v", err)
	}
	_ = ctl.Close()

	dataf, err = c.FOpen("/devices/by-id/robot-001/functions/echo/"+id+"/data", p.OREAD)
	if err != nil {
		t.Fatalf("reopen data: %v", err)
	}
	got, err := io.ReadAll(dataf)
	_ = dataf.Close()
	if err != nil {
		t.Fatalf("read data: %v", err)
	}
	if string(got) != "hi\n" {
		t.Fatalf("data=%q", string(got))
	}
}

func TestDeviceConnect_FunctionErrorFile(t *testing.T) {
	backend := &fakeBackend{
		devices: []Device{
			{
				ID:     "robot-001",
				Type:   "robot",
				Meta:   "id=robot-001 type=robot",
				Status: "ok",
				Functions: []Function{
					{Name: "fail", About: "Fail.", Schema: "bytes"},
				},
			},
		},
		invoke: func(deviceID, fn string, payload []byte) ([]byte, error) {
			return nil, errors.New("boom")
		},
	}
	addr, stop := startDeviceConnectServer(t, backend)
	defer stop()

	c, cleanup := mountClient(t, addr)
	defer cleanup()

	cl, err := c.FOpen("/devices/by-id/robot-001/functions/fail/clone", p.OREAD)
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	idb, err := io.ReadAll(cl)
	_ = cl.Close()
	if err != nil {
		t.Fatalf("read clone: %v", err)
	}
	id := strings.TrimSpace(string(idb))
	if id == "" {
		t.Fatalf("empty clone id")
	}

	ctl, err := c.FOpen("/devices/by-id/robot-001/functions/fail/"+id+"/ctl", p.OWRITE)
	if err != nil {
		t.Fatalf("open ctl: %v", err)
	}
	_, err = ctl.Write([]byte("call\n"))
	_ = ctl.Close()
	if err == nil {
		t.Fatalf("expected ctl call to fail")
	}

	ef, err := c.FOpen("/devices/by-id/robot-001/functions/fail/"+id+"/error", p.OREAD)
	if err != nil {
		t.Fatalf("open error: %v", err)
	}
	eb, err := io.ReadAll(ef)
	_ = ef.Close()
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if !strings.Contains(string(eb), "boom") {
		t.Fatalf("error=%q", string(eb))
	}
}

func TestDeviceConnect_FunctionStream(t *testing.T) {
	backend := &fakeBackend{
		devices: []Device{
			{
				ID:     "robot-001",
				Type:   "robot",
				Meta:   "id=robot-001 type=robot",
				Status: "ok",
				Functions: []Function{
					{Name: "streamy", About: "Stream.", Schema: "bytes"},
				},
			},
		},
		stream: func(deviceID, fn string, payload []byte) (<-chan []byte, error) {
			ch := make(chan []byte, 3)
			ch <- []byte("a")
			ch <- []byte("b")
			ch <- []byte("c\n")
			close(ch)
			return ch, nil
		},
	}
	addr, stop := startDeviceConnectServer(t, backend)
	defer stop()

	c, cleanup := mountClient(t, addr)
	defer cleanup()

	cl, err := c.FOpen("/devices/by-id/robot-001/functions/streamy/clone", p.OREAD)
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	idb, err := io.ReadAll(cl)
	_ = cl.Close()
	if err != nil {
		t.Fatalf("read clone: %v", err)
	}
	id := strings.TrimSpace(string(idb))
	if id == "" {
		t.Fatalf("empty clone id")
	}

	ctl, err := c.FOpen("/devices/by-id/robot-001/functions/streamy/"+id+"/ctl", p.OWRITE)
	if err != nil {
		t.Fatalf("open ctl: %v", err)
	}
	if _, err := ctl.Write([]byte("stream\n")); err != nil {
		_ = ctl.Close()
		t.Fatalf("ctl stream: %v", err)
	}
	_ = ctl.Close()

	sf, err := c.FOpen("/devices/by-id/robot-001/functions/streamy/"+id+"/stream", p.OREAD)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	sb, err := io.ReadAll(sf)
	_ = sf.Close()
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if string(sb) != "abc\n" {
		t.Fatalf("stream=%q", string(sb))
	}
}

// Connection-scoped cleanup: dirs allocated by a connection are reaped when
// that connection drops.
func TestDeviceConnect_DisconnectCleansUpCalls(t *testing.T) {
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
	}
	addr, stop := startDeviceConnectServer(t, backend)
	defer stop()

	// Connection A: allocate several call dirs and never close them.
	cA, cleanA := mountClient(t, addr)
	const N = 5
	var ids []string
	for i := 0; i < N; i++ {
		cl, err := cA.FOpen("/devices/by-id/robot-001/functions/echo/clone", p.OREAD)
		if err != nil {
			t.Fatalf("clone %d: %v", i, err)
		}
		idb, err := io.ReadAll(cl)
		_ = cl.Close()
		if err != nil {
			t.Fatalf("read clone %d: %v", i, err)
		}
		ids = append(ids, strings.TrimSpace(string(idb)))
	}

	// Sanity: every dir is reachable while A is connected.
	for _, id := range ids {
		f, err := cA.FOpen("/devices/by-id/robot-001/functions/echo/"+id+"/data", p.OREAD)
		if err != nil {
			t.Fatalf("dir %q should be reachable while A is connected: %v", id, err)
		}
		_ = f.Close()
	}

	// Drop A entirely. ConnClosed must reap everything A allocated.
	cleanA()

	// Give the server a brief moment to run its ConnClosed hook (it runs
	// inline on the conn's goroutine, but the close itself races against
	// the client returning from Unmount).
	deadline := time.Now().Add(2 * time.Second)
	cB, cleanB := mountClient(t, addr)
	defer cleanB()
	for {
		stillThere := 0
		for _, id := range ids {
			f, err := cB.FOpen("/devices/by-id/robot-001/functions/echo/"+id+"/data", p.OREAD)
			if err == nil {
				stillThere++
				_ = f.Close()
			}
		}
		if stillThere == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after A disconnected, %d/%d dirs still reachable from B — ConnClosed did not reap", stillThere, N)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDeviceConnect_DeviceEventsReplay(t *testing.T) {
	src := make(chan Event, 8)
	backend := &fakeBackend{
		devices: []Device{
			{
				ID:     "sensor-001",
				Type:   "sensor",
				Meta:   "id=sensor-001 type=sensor",
				Status: "ok",
			},
		},
		events: map[string]chan Event{
			"sensor-001": src,
		},
	}
	addr, stop := startDeviceConnectServer(t, backend)
	defer stop()

	// Emit an event after server is up.
	src <- Event{DeviceID: "sensor-001", Topic: "event.alert", Payload: []byte("hot")}

	c, cleanup := mountClient(t, addr)
	defer cleanup()

	f, err := c.FOpen("/devices/by-id/sensor-001/events/replay", p.OREAD)
	if err != nil {
		t.Fatalf("open replay: %v", err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read replay: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "event.alert") || !strings.Contains(s, "hot") {
		t.Fatalf("replay=%q", s)
	}
}

func TestDeviceConnect_ValueEventsReplay(t *testing.T) {
	src := make(chan Event, 8)
	backend := &fakeBackend{
		devices: []Device{
			{
				ID:     "sensor-001",
				Type:   "sensor",
				Meta:   "id=sensor-001 type=sensor",
				Status: "ok",
				Values: []Value{
					{Name: "temp", About: "Temperature", Unit: "C"},
				},
			},
		},
		values: map[string]map[string][]byte{
			"sensor-001": {"temp": []byte("21.0")},
		},
		vEvents: map[string]map[string]chan Event{
			"sensor-001": {"temp": src},
		},
	}
	addr, stop := startDeviceConnectServer(t, backend)
	defer stop()

	src <- Event{DeviceID: "sensor-001", Topic: "value.temp", Payload: []byte("21.0")}

	c, cleanup := mountClient(t, addr)
	defer cleanup()

	f, err := c.FOpen("/devices/by-id/sensor-001/values/temp/events/replay", p.OREAD)
	if err != nil {
		t.Fatalf("open replay: %v", err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read replay: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "value.temp") || !strings.Contains(s, "21.0") {
		t.Fatalf("replay=%q", s)
	}
}
