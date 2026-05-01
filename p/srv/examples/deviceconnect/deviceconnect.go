// deviceconnect is a synthetic filesystem that projects a Device Connect-style
// device/function model (deviceconnect.dev) into a 9P namespace.
//
// This example intentionally focuses on discoverability, hierarchy, and RPC-like
// function invocation via file reads/writes. It does not implement Device Connect's
// transport/security stack.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lionkov/go9p/p"
	"github.com/lionkov/go9p/p/srv"
)

var (
	addr  = flag.String("addr", ":5642", "network address")
	debug = flag.Bool("d", false, "print debug messages")
)

type Device struct {
	ID        string
	Type      string
	Meta      string
	Status    string
	Values    []Value
	Functions []Function
}

type Function struct {
	Name   string
	About  string
	Schema string
}

type Value struct {
	Name  string
	About string
	Unit  string
}

type Event struct {
	DeviceID string
	Topic    string
	Payload  []byte
	Time     time.Time
}

type Backend interface {
	ListDevices(ctx context.Context) ([]Device, error)
	ReadValue(ctx context.Context, deviceID string, name string) ([]byte, error)
	Invoke(ctx context.Context, deviceID string, fn string, payload []byte) ([]byte, error)
	InvokeStream(ctx context.Context, deviceID string, fn string, payload []byte) (<-chan []byte, error)

	// SubscribeDeviceEvents returns a stream of events for a device until ctx is
	// cancelled. Implementations should return (nil, nil) when events are not supported.
	SubscribeDeviceEvents(ctx context.Context, deviceID string) (<-chan Event, error)

	// SubscribeValueEvents returns a stream of events for a particular device value.
	// Implementations should return (nil, nil) when not supported.
	SubscribeValueEvents(ctx context.Context, deviceID string, valueName string) (<-chan Event, error)
}

// ---------- simple file helpers ----------

type roTextFile struct {
	srv.File
	data []byte
}

func (f *roTextFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	if offset >= uint64(len(f.data)) {
		return 0, nil
	}
	n := copy(buf, f.data[offset:])
	return n, nil
}

type eventLog struct {
	mu   sync.Mutex
	buf  []byte
	keep int
}

func newEventLog(keep int) *eventLog {
	if keep <= 0 {
		keep = 64 * 1024
	}
	return &eventLog{keep: keep}
}

func (l *eventLog) appendLine(line []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, line...)
	if len(l.buf) <= l.keep {
		return
	}
	// Drop from the front; best-effort keep UTF-8/text integrity by trimming to the next '\n'.
	cut := len(l.buf) - l.keep
	if cut < 0 {
		cut = 0
	}
	if cut >= len(l.buf) {
		l.buf = nil
		return
	}
	// Seek to the next newline boundary.
	nl := bytesIndexByte(l.buf[cut:], '\n')
	if nl >= 0 {
		cut = cut + nl + 1
	}
	l.buf = append([]byte(nil), l.buf[cut:]...)
}

func bytesIndexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

type eventLogFile struct {
	srv.File
	log *eventLog
}

func (f *eventLogFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	f.log.mu.Lock()
	defer f.log.mu.Unlock()
	if offset >= uint64(len(f.log.buf)) {
		return 0, nil
	}
	return copy(buf, f.log.buf[offset:]), nil
}

type valueFile struct {
	srv.File
	backend  Backend
	deviceID string
	name     string
}

func (f *valueFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b, err := f.backend.ReadValue(ctx, f.deviceID, f.name)
	if err != nil {
		// Model as read error (like a sensor not available).
		return 0, &p.Error{Err: err.Error(), Errornum: 0}
	}
	out := b
	if len(out) != 0 && out[len(out)-1] != '\n' {
		out = append(append([]byte(nil), out...), '\n')
	}
	if offset >= uint64(len(out)) {
		return 0, nil
	}
	return copy(buf, out[offset:]), nil
}

// ---------- function call instances (Plan 9 style: clone/ctl/data/error) ----------

type callInstance struct {
	mu sync.Mutex

	req  []byte
	resp []byte
	err  string

	stream *eventLog
}

type callDataFile struct {
	srv.File
	inst *callInstance
}

func (f *callDataFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	f.inst.mu.Lock()
	defer f.inst.mu.Unlock()
	if offset >= uint64(len(f.inst.resp)) {
		return 0, nil
	}
	return copy(buf, f.inst.resp[offset:]), nil
}

func (f *callDataFile) Write(fid *srv.FFid, data []byte, offset uint64) (int, error) {
	f.inst.mu.Lock()
	defer f.inst.mu.Unlock()

	need := int(offset) + len(data)
	if need < 0 {
		return 0, &p.Error{Err: "invalid offset", Errornum: 0}
	}
	if need > len(f.inst.req) {
		n := make([]byte, need)
		copy(n, f.inst.req)
		f.inst.req = n
	}
	copy(f.inst.req[offset:], data)
	return len(data), nil
}

type callErrFile struct {
	srv.File
	inst *callInstance
}

func (f *callErrFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	f.inst.mu.Lock()
	defer f.inst.mu.Unlock()
	out := []byte(f.inst.err)
	if len(out) != 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if offset >= uint64(len(out)) {
		return 0, nil
	}
	return copy(buf, out[offset:]), nil
}

type callCtlFile struct {
	srv.File
	backend  Backend
	deviceID string
	fn       string
	inst     *callInstance

	mu      sync.Mutex
	opened  map[*srv.Fid]bool
	opens   int
	removed bool
	dir     *srv.File
}

func (f *callCtlFile) Write(fid *srv.FFid, data []byte, offset uint64) (int, error) {
	if offset != 0 {
		return 0, &p.Error{Err: "ctl does not support non-zero offset writes", Errornum: 0}
	}
	cmd := strings.TrimSpace(string(data))
	switch cmd {
	case "call", "invoke", "run":
		// Snapshot request bytes.
		f.inst.mu.Lock()
		req := append([]byte(nil), f.inst.req...)
		f.inst.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := f.backend.Invoke(ctx, f.deviceID, f.fn, req)
		if err != nil {
			f.inst.mu.Lock()
			f.inst.err = err.Error()
			f.inst.resp = nil
			f.inst.mu.Unlock()
			return 0, &p.Error{Err: err.Error(), Errornum: 0}
		}
		f.inst.mu.Lock()
		f.inst.err = ""
		f.inst.resp = resp
		f.inst.mu.Unlock()
		return len(data), nil
	case "stream":
		f.inst.mu.Lock()
		req := append([]byte(nil), f.inst.req...)
		f.inst.err = ""
		f.inst.resp = nil
		if f.inst.stream == nil {
			f.inst.stream = newEventLog(256 * 1024)
		}
		// Reset stream buffer for this run.
		f.inst.stream.mu.Lock()
		f.inst.stream.buf = nil
		f.inst.stream.mu.Unlock()
		slog := f.inst.stream
		f.inst.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ch, err := f.backend.InvokeStream(ctx, f.deviceID, f.fn, req)
		if err != nil {
			f.inst.mu.Lock()
			f.inst.err = err.Error()
			f.inst.mu.Unlock()
			return 0, &p.Error{Err: err.Error(), Errornum: 0}
		}
		if ch == nil {
			err := fmt.Errorf("stream not supported")
			f.inst.mu.Lock()
			f.inst.err = err.Error()
			f.inst.mu.Unlock()
			return 0, &p.Error{Err: err.Error(), Errornum: 0}
		}
		for chunk := range ch {
			if len(chunk) == 0 {
				continue
			}
			slog.appendLine(chunk)
		}
		// For convenience, also publish the concatenated stream as resp.
		f.inst.mu.Lock()
		slog.mu.Lock()
		f.inst.resp = append([]byte(nil), slog.buf...)
		slog.mu.Unlock()
		f.inst.mu.Unlock()
		return len(data), nil
	case "reset":
		f.inst.mu.Lock()
		f.inst.req = nil
		f.inst.resp = nil
		f.inst.err = ""
		f.inst.mu.Unlock()
		return len(data), nil
	default:
		return 0, &p.Error{Err: fmt.Sprintf("unknown ctl command %q", cmd), Errornum: 0}
	}
}

func (f *callCtlFile) Open(fid *srv.FFid, mode uint8) error {
	f.mu.Lock()
	if f.opened == nil {
		f.opened = make(map[*srv.Fid]bool, 1)
	}
	f.opened[fid.Fid] = true
	f.opens++
	f.mu.Unlock()
	return nil
}

func (f *callCtlFile) Clunk(fid *srv.FFid) error {
	f.mu.Lock()
	// Only count opens for fids that were actually opened. Some clients (notably
	// the Linux kernel 9p client) may walk+clunk fids without a preceding open.
	if f.opened != nil && f.opened[fid.Fid] {
		delete(f.opened, fid.Fid)
	} else {
		f.mu.Unlock()
		return nil
	}
	if f.opens > 0 {
		f.opens--
	}
	doRemove := f.opens == 0 && !f.removed
	if doRemove {
		f.removed = true
	}
	dir := f.dir
	f.mu.Unlock()

	if doRemove && dir != nil {
		dir.Remove()
	}
	return nil
}

type funcCallState struct {
	mu   sync.Mutex
	next int
}

type funcCloneFile struct {
	srv.File
	state    *funcCallState
	parent   *srv.File
	user     p.User
	backend  Backend
	deviceID string
	fn       string
}

func (f *funcCloneFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	if offset != 0 {
		return 0, nil
	}
	f.state.mu.Lock()
	f.state.next++
	id := f.state.next
	f.state.mu.Unlock()

	instDir := new(srv.File)
	name := fmt.Sprintf("%d", id)
	if err := instDir.Add(f.parent, name, f.user, nil, p.DMDIR|0o755, nil); err != nil {
		return 0, err
	}

	inst := &callInstance{}
	ctl := &callCtlFile{backend: f.backend, deviceID: f.deviceID, fn: f.fn, inst: inst}
	ctl.dir = instDir
	if err := ctl.Add(instDir, "ctl", f.user, nil, 0o666, ctl); err != nil {
		instDir.Remove()
		return 0, err
	}
	dataf := &callDataFile{inst: inst}
	if err := dataf.Add(instDir, "data", f.user, nil, 0o666, dataf); err != nil {
		instDir.Remove()
		return 0, err
	}
	errf := &callErrFile{inst: inst}
	if err := errf.Add(instDir, "error", f.user, nil, 0o444, errf); err != nil {
		instDir.Remove()
		return 0, err
	}

	streamf := &eventLogFile{log: newEventLog(256 * 1024)}
	inst.stream = streamf.log
	if err := streamf.Add(instDir, "stream", f.user, nil, 0o444, streamf); err != nil {
		instDir.Remove()
		return 0, err
	}

	out := []byte(name + "\n")
	if len(buf) < len(out) {
		instDir.Remove()
		return 0, &p.Error{Err: "buffer too small", Errornum: 0}
	}
	copy(buf, out)
	return len(out), nil
}

// ---------- filesystem construction ----------

type DCFS struct {
	backend Backend
	user    p.User

	mu      sync.Mutex
	root    *srv.File
	devices *srv.File
	byID    *srv.File

	// We keep our own index of created directories because srv.File's child list
	// is internal to the srv package.
	deviceDirs map[string]*srv.File

	// Per-device event subscription cancels (best-effort cleanup on refresh).
	eventCancels map[string]context.CancelFunc

	// Per-device-value event cancels (best-effort cleanup on refresh).
	valueEventCancels map[string]context.CancelFunc
}

func buildDeviceConnectFS(backend Backend) (*DCFS, error) {
	fs := &DCFS{
		backend:           backend,
		user:              p.OsUsers.Uid2User(os.Geteuid()),
		deviceDirs:        make(map[string]*srv.File),
		eventCancels:      make(map[string]context.CancelFunc),
		valueEventCancels: make(map[string]context.CancelFunc),
	}

	fs.root = new(srv.File)
	if err := fs.root.Add(nil, "/", fs.user, nil, p.DMDIR|0o777, nil); err != nil {
		return nil, err
	}

	fs.devices = new(srv.File)
	if err := fs.devices.Add(fs.root, "devices", fs.user, nil, p.DMDIR|0o755, nil); err != nil {
		return nil, err
	}

	disc := &discoverFile{fs: fs}
	if err := disc.Add(fs.devices, "discover", fs.user, nil, 0o666, disc); err != nil {
		return nil, err
	}

	fs.byID = new(srv.File)
	if err := fs.byID.Add(fs.devices, "by-id", fs.user, nil, p.DMDIR|0o755, nil); err != nil {
		return nil, err
	}

	if err := fs.refreshLocked(context.Background()); err != nil {
		return nil, err
	}

	return fs, nil
}

type discoverFile struct {
	srv.File
	fs *DCFS
}

func (f *discoverFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	var ids []string
	for id := range f.fs.deviceDirs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		// The directory itself contains richer metadata; keep this index compact.
		b.WriteString(id)
		b.WriteString("\n")
	}
	out := []byte(b.String())
	if offset >= uint64(len(out)) {
		return 0, nil
	}
	return copy(buf, out[offset:]), nil
}

func (f *discoverFile) Write(fid *srv.FFid, data []byte, offset uint64) (int, error) {
	if offset != 0 {
		return 0, &p.Error{Err: "discover does not support non-zero offset writes", Errornum: 0}
	}
	f.fs.mu.Lock()
	_ = f.fs.refreshLocked(context.Background())
	f.fs.mu.Unlock()
	return len(data), nil
}

func (fs *DCFS) refreshLocked(ctx context.Context) error {
	devs, err := fs.backend.ListDevices(ctx)
	if err != nil {
		return err
	}

	// Clear existing.
	for _, dir := range fs.deviceDirs {
		dir.Remove()
	}
	clear(fs.deviceDirs)

	for _, cancel := range fs.eventCancels {
		cancel()
	}
	clear(fs.eventCancels)
	for _, cancel := range fs.valueEventCancels {
		cancel()
	}
	clear(fs.valueEventCancels)

	// Rebuild.
	sort.Slice(devs, func(i, j int) bool { return devs[i].ID < devs[j].ID })
	for _, d := range devs {
		if err := fs.addDeviceLocked(d); err != nil {
			return err
		}
	}
	return nil
}

func (fs *DCFS) addDeviceLocked(d Device) error {
	if d.ID == "" {
		return fmt.Errorf("device missing ID")
	}

	devDir := new(srv.File)
	if err := devDir.Add(fs.byID, d.ID, fs.user, nil, p.DMDIR|0o755, nil); err != nil {
		return err
	}
	fs.deviceDirs[d.ID] = devDir

	meta := &roTextFile{data: []byte(strings.TrimSpace(d.Meta) + "\n")}
	if err := meta.Add(devDir, "meta", fs.user, nil, 0o444, meta); err != nil {
		return err
	}

	status := &roTextFile{data: []byte(strings.TrimSpace(d.Status) + "\n")}
	if err := status.Add(devDir, "status", fs.user, nil, 0o444, status); err != nil {
		return err
	}

	eventsDir := new(srv.File)
	if err := eventsDir.Add(devDir, "events", fs.user, nil, p.DMDIR|0o755, nil); err != nil {
		return err
	}
	logbuf := newEventLog(64 * 1024)

	replay := &eventLogFile{log: logbuf}
	if err := replay.Add(eventsDir, "replay", fs.user, nil, 0o444, replay); err != nil {
		return err
	}
	stream := &eventLogFile{log: logbuf}
	if err := stream.Add(eventsDir, "stream", fs.user, nil, 0o444, stream); err != nil {
		return err
	}

	// Subscribe in the background if supported by the backend.
	evctx, cancel := context.WithCancel(context.Background())
	ch, err := fs.backend.SubscribeDeviceEvents(evctx, d.ID)
	if err == nil && ch != nil {
		fs.eventCancels[d.ID] = cancel
		go func() {
			for ev := range ch {
				ts := ev.Time
				if ts.IsZero() {
					ts = time.Now()
				}
				topic := strings.TrimSpace(ev.Topic)
				if topic == "" {
					topic = "event"
				}
				payload := strings.TrimRight(string(ev.Payload), "\n")
				line := fmt.Sprintf("%s\t%s\t%s\n", ts.UTC().Format(time.RFC3339Nano), topic, payload)
				logbuf.appendLine([]byte(line))
			}
		}()
	} else {
		cancel()
	}

	// Values: device-level readings (separate from functions).
	valuesDir := new(srv.File)
	if err := valuesDir.Add(devDir, "values", fs.user, nil, p.DMDIR|0o755, nil); err != nil {
		return err
	}
	sort.Slice(d.Values, func(i, j int) bool { return d.Values[i].Name < d.Values[j].Name })
	for _, v := range d.Values {
		if strings.TrimSpace(v.Name) == "" {
			continue
		}
		vdir := new(srv.File)
		if err := vdir.Add(valuesDir, v.Name, fs.user, nil, p.DMDIR|0o755, nil); err != nil {
			return err
		}
		about := strings.TrimSpace(v.About)
		if about == "" {
			about = v.Name
		}
		if v.Unit != "" {
			about = about + " (unit: " + v.Unit + ")"
		}
		vabout := &roTextFile{data: []byte(about + "\n")}
		if err := vabout.Add(vdir, "about", fs.user, nil, 0o444, vabout); err != nil {
			return err
		}

		vf := &valueFile{backend: fs.backend, deviceID: d.ID, name: v.Name}
		if err := vf.Add(vdir, "value", fs.user, nil, 0o444, vf); err != nil {
			return err
		}

		vedir := new(srv.File)
		if err := vedir.Add(vdir, "events", fs.user, nil, p.DMDIR|0o755, nil); err != nil {
			return err
		}
		vlog := newEventLog(64 * 1024)
		vreplay := &eventLogFile{log: vlog}
		if err := vreplay.Add(vedir, "replay", fs.user, nil, 0o444, vreplay); err != nil {
			return err
		}
		vstream := &eventLogFile{log: vlog}
		if err := vstream.Add(vedir, "stream", fs.user, nil, 0o444, vstream); err != nil {
			return err
		}

		evctx, cancel := context.WithCancel(context.Background())
		ch, err := fs.backend.SubscribeValueEvents(evctx, d.ID, v.Name)
		if err == nil && ch != nil {
			fs.valueEventCancels[d.ID+"|"+v.Name] = cancel
			go func() {
				for ev := range ch {
					ts := ev.Time
					if ts.IsZero() {
						ts = time.Now()
					}
					topic := strings.TrimSpace(ev.Topic)
					if topic == "" {
						topic = "event"
					}
					payload := strings.TrimRight(string(ev.Payload), "\n")
					line := fmt.Sprintf("%s\t%s\t%s\n", ts.UTC().Format(time.RFC3339Nano), topic, payload)
					vlog.appendLine([]byte(line))
				}
			}()
		} else {
			cancel()
		}
	}

	funcsDir := new(srv.File)
	if err := funcsDir.Add(devDir, "functions", fs.user, nil, p.DMDIR|0o755, nil); err != nil {
		return err
	}

	sort.Slice(d.Functions, func(i, j int) bool { return d.Functions[i].Name < d.Functions[j].Name })
	for _, fn := range d.Functions {
		if fn.Name == "" {
			continue
		}
		fnDir := new(srv.File)
		if err := fnDir.Add(funcsDir, fn.Name, fs.user, nil, p.DMDIR|0o755, nil); err != nil {
			return err
		}

		about := &roTextFile{data: []byte(strings.TrimSpace(fn.About) + "\n")}
		if err := about.Add(fnDir, "about", fs.user, nil, 0o444, about); err != nil {
			return err
		}
		schema := &roTextFile{data: []byte(strings.TrimSpace(fn.Schema) + "\n")}
		if err := schema.Add(fnDir, "schema", fs.user, nil, 0o444, schema); err != nil {
			return err
		}

		st := &funcCallState{}
		cl := &funcCloneFile{
			state:    st,
			parent:   fnDir,
			user:     fs.user,
			backend:  fs.backend,
			deviceID: d.ID,
			fn:       fn.Name,
		}
		if err := cl.Add(fnDir, "clone", fs.user, nil, 0o444, cl); err != nil {
			return err
		}
	}

	_ = d.Type // reserved for future indexing/filters
	return nil
}

// ---------- demo backend ----------

type demoBackend struct {
	mu      sync.Mutex
	devices []Device
}

func (b *demoBackend) ListDevices(ctx context.Context) ([]Device, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Device, len(b.devices))
	copy(out, b.devices)
	return out, nil
}

func (b *demoBackend) ReadValue(ctx context.Context, deviceID string, name string) ([]byte, error) {
	// Demo: return a stable placeholder; real integrations would read from the mesh/device.
	return []byte("0"), nil
}

func (b *demoBackend) Invoke(ctx context.Context, deviceID string, fn string, payload []byte) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.devices {
		if d.ID != deviceID {
			continue
		}
		for _, f := range d.Functions {
			if f.Name != fn {
				continue
			}
			// Demo semantics:
			// - echo(payload) returns payload
			// - ping returns "pong"
			switch fn {
			case "echo":
				return payload, nil
			case "ping":
				return []byte("pong\n"), nil
			default:
				return []byte(fmt.Sprintf("ok: %s.%s (%d bytes)\n", deviceID, fn, len(payload))), nil
			}
		}
		return nil, fmt.Errorf("unknown function %q for device %q", fn, deviceID)
	}
	return nil, fmt.Errorf("unknown device %q", deviceID)
}

func (b *demoBackend) InvokeStream(ctx context.Context, deviceID string, fn string, payload []byte) (<-chan []byte, error) {
	// Demo backend doesn't stream.
	return nil, nil
}

func (b *demoBackend) SubscribeDeviceEvents(ctx context.Context, deviceID string) (<-chan Event, error) {
	// Demo backend doesn't emit events.
	return nil, nil
}

func (b *demoBackend) SubscribeValueEvents(ctx context.Context, deviceID string, valueName string) (<-chan Event, error) {
	// Demo backend doesn't emit value events.
	return nil, nil
}

func main() {
	flag.Parse()

	backend := &demoBackend{
		devices: []Device{
			{
				ID:     "sensor-001",
				Type:   "sensor",
				Meta:   "id=sensor-001 type=sensor",
				Status: "ok",
				Values: []Value{
					{Name: "temp", About: "Temperature", Unit: "C"},
					{Name: "humidity", About: "Relative humidity", Unit: "%"},
				},
				Functions: []Function{
					{Name: "get_reading", About: "Return current reading (demo).", Schema: "write: ignored\nread: returns a demo reading"},
					{Name: "ping", About: "Liveness check.", Schema: "write: optional\nread: pong"},
				},
			},
			{
				ID:     "robot-001",
				Type:   "robot",
				Meta:   "id=robot-001 type=robot",
				Status: "ok",
				Values: []Value{
					{Name: "battery", About: "Battery level", Unit: "%"},
				},
				Functions: []Function{
					{Name: "echo", About: "Echo the request bytes.", Schema: "write: arbitrary bytes\nread: same bytes"},
					{Name: "move", About: "Move the robot (demo stub).", Schema: "write: \"x y\" or JSON\nread: ok"},
				},
			},
		},
	}

	fs, err := buildDeviceConnectFS(backend)
	if err != nil {
		log.Fatalf("build: %v", err)
	}

	s := srv.NewFileSrv(fs.root)
	s.Dotu = true
	if *debug {
		s.Debuglevel = 1
	}
	s.Start(s)

	if err := s.StartNetListener("tcp", *addr); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
