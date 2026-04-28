## `netfs` CLI helpers

This repo includes `netfs` (`p/srv/examples/netfs`), a synthetic filesystem that models parts of Plan 9’s `/net`.

There are two CLI tools to make it easier to use:

- **go9p-client**: `cmd/go9p-netfs` (talks 9P directly)
- **kernel-mounted**: `cmd/go9p-knetfs` (talks to a mounted filesystem tree)

### Run the server

```bash
go run ./p/srv/examples/netfs
```

### `tcp-dial` (nc-like)

#### go9p-client

```bash
go run ./cmd/go9p-netfs tcp-dial example.com!80
```

#### kernel-mounted

Assuming you mounted the server at `/mnt/9p` and it provides `/net` there:

```bash
go run ./cmd/go9p-knetfs tcp-dial example.com!80
```

### `tcp-alloc` / `tcp-connect`

The kernel-mounted tool also exposes small, composable steps:

```bash
go run ./cmd/go9p-knetfs tcp-alloc
go run ./cmd/go9p-knetfs tcp-connect example.com!80
```

