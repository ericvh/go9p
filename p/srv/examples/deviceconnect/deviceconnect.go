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
	addr  = flag.String("addr", ":5640", "network address")
	debug = flag.Bool("d", false, "print debug messages")
)

type Device struct {
	ID        string
	Type      string
	Meta      string
	Status    string
	Functions []Function
}

type Function struct {
	Name   string
	About  string
	Schema string
}

type Backend interface {
	ListDevices(ctx context.Context) ([]Device, error)
	Invoke(ctx context.Context, deviceID string, fn string, payload []byte) ([]byte, error)
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

// ---------- invoke/result files ----------

type invokeFile struct {
	srv.File
	backend  Backend
	deviceID string
	fn       string

	mu         sync.Mutex
	lastResult []byte
	lastErr    string
}

func (f *invokeFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []byte
	if f.lastErr != "" {
		out = []byte("error: " + f.lastErr + "\n")
	} else if len(f.lastResult) == 0 {
		out = []byte("")
	} else {
		out = f.lastResult
		if len(out) == 0 || out[len(out)-1] != '\n' {
			out = append(append([]byte(nil), out...), '\n')
		}
	}

	if offset >= uint64(len(out)) {
		return 0, nil
	}
	return copy(buf, out[offset:]), nil
}

func (f *invokeFile) Write(fid *srv.FFid, data []byte, offset uint64) (int, error) {
	if offset != 0 {
		// Keep semantics simple: each write is a whole invocation.
		return 0, &p.Error{Err: "invoke does not support non-zero offset writes", Errornum: 0}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := f.backend.Invoke(ctx, f.deviceID, f.fn, data)

	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.lastErr = err.Error()
		f.lastResult = nil
		return len(data), nil
	}
	f.lastErr = ""
	f.lastResult = res
	return len(data), nil
}

type resultFile struct {
	srv.File
	invoke *invokeFile
}

func (f *resultFile) Read(fid *srv.FFid, buf []byte, offset uint64) (int, error) {
	f.invoke.mu.Lock()
	defer f.invoke.mu.Unlock()

	out := f.invoke.lastResult
	if out == nil {
		out = []byte("")
	}
	if len(out) != 0 && out[len(out)-1] != '\n' {
		out = append(append([]byte(nil), out...), '\n')
	}
	if offset >= uint64(len(out)) {
		return 0, nil
	}
	return copy(buf, out[offset:]), nil
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
}

func buildDeviceConnectFS(backend Backend) (*DCFS, error) {
	fs := &DCFS{
		backend:    backend,
		user:       p.OsUsers.Uid2User(os.Geteuid()),
		deviceDirs: make(map[string]*srv.File),
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
	// Reading discover is also a convenient place to refresh before listing.
	f.fs.mu.Lock()
	_ = f.fs.refreshLocked(context.Background())
	f.fs.mu.Unlock()

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

		inv := &invokeFile{backend: fs.backend, deviceID: d.ID, fn: fn.Name}
		if err := inv.Add(fnDir, "invoke", fs.user, nil, 0o666, inv); err != nil {
			return err
		}
		res := &resultFile{invoke: inv}
		if err := res.Add(fnDir, "result", fs.user, nil, 0o444, res); err != nil {
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

func main() {
	flag.Parse()

	backend := &demoBackend{
		devices: []Device{
			{
				ID:     "sensor-001",
				Type:   "sensor",
				Meta:   "id=sensor-001 type=sensor",
				Status: "ok",
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
