## `deviceconnect` CLI helpers

This repo includes `deviceconnect` (`p/srv/examples/deviceconnect`), a synthetic filesystem that projects a
Device Connect-style device/function model into a 9P namespace.

There are two CLI tools to make it easier to use:

- **go9p-client**: `cmd/go9p-deviceconnect` (talks 9P directly)
- **kernel-mounted**: `cmd/go9p-kdeviceconnect` (talks to a mounted filesystem tree)

### Run the server

```bash
go run ./p/srv/examples/deviceconnect
```

### Discover devices

#### go9p-client

```bash
go run ./cmd/go9p-deviceconnect discover
```

#### kernel-mounted

If the mounted tree exposes `/devices` at `/mnt/9p/devices`:

```bash
go run ./cmd/go9p-kdeviceconnect discover
```

### Read metadata/status/value

```bash
go run ./cmd/go9p-deviceconnect meta   -id robot-001
go run ./cmd/go9p-deviceconnect status -id robot-001
go run ./cmd/go9p-deviceconnect value  -id sensor-001 -name temp
```

Kernel-mounted equivalents:

```bash
go run ./cmd/go9p-kdeviceconnect meta   -id robot-001
go run ./cmd/go9p-kdeviceconnect status -id robot-001
go run ./cmd/go9p-kdeviceconnect value  -id sensor-001 -name temp
```

### Invoke a function (`call`)

```bash
go run ./cmd/go9p-deviceconnect call -id robot-001 -fn echo -payload "hello"
```

Kernel-mounted:

```bash
go run ./cmd/go9p-kdeviceconnect call -id robot-001 -fn echo -payload "hello"
```

### Streaming mode

If a backend supports it, you can request streaming mode:

```bash
go run ./cmd/go9p-deviceconnect call -id robot-001 -fn <fn> -stream
```

