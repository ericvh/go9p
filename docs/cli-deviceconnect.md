## `deviceconnect` CLI helpers

This repo includes `deviceconnect` (`p/srv/examples/deviceconnect`), a synthetic filesystem that projects a
Device Connect-style device/function model into a 9P namespace.

There are two CLI tools to make it easier to use:

- **go9p-client**: `cmd/go9p-deviceconnect` (talks 9P directly)
- **kernel-mounted**: `cmd/go9p-kdeviceconnect` (talks to a mounted filesystem tree)

### Run the server

```bash
go run ./p/srv/examples/deviceconnect -addr 127.0.0.1:5640
```

### Discover devices

#### go9p-client

```bash
go run ./cmd/go9p-deviceconnect discover -addr 127.0.0.1:5640
```

#### kernel-mounted

If the mounted tree exposes `/devices` at `/mnt/9p/devices`:

```bash
go run ./cmd/go9p-kdeviceconnect -root /mnt/9p/devices discover
```

### Read metadata/status/value

```bash
go run ./cmd/go9p-deviceconnect meta   -addr 127.0.0.1:5640 -id robot-001
go run ./cmd/go9p-deviceconnect status -addr 127.0.0.1:5640 -id robot-001
go run ./cmd/go9p-deviceconnect value  -addr 127.0.0.1:5640 -id sensor-001 -name temp
```

Kernel-mounted equivalents:

```bash
go run ./cmd/go9p-kdeviceconnect -root /mnt/9p/devices meta   -id robot-001
go run ./cmd/go9p-kdeviceconnect -root /mnt/9p/devices status -id robot-001
go run ./cmd/go9p-kdeviceconnect -root /mnt/9p/devices value  -id sensor-001 -name temp
```

### Invoke a function (`call`)

```bash
go run ./cmd/go9p-deviceconnect call -addr 127.0.0.1:5640 -id robot-001 -fn echo -payload "hello"
```

Kernel-mounted:

```bash
go run ./cmd/go9p-kdeviceconnect -root /mnt/9p/devices call -id robot-001 -fn echo -payload "hello"
```

### Streaming mode

If a backend supports it, you can request streaming mode:

```bash
go run ./cmd/go9p-deviceconnect call -addr 127.0.0.1:5640 -id robot-001 -fn <fn> -stream
```

