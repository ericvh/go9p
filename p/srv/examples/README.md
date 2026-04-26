## go9p server examples (`p/srv/examples`)

This directory contains small example 9P servers built on top of the `p/srv` framework.
They are intentionally minimal and are meant to show how to:

- Create an in-memory or synthetic tree of `srv.File` nodes
- Implement per-node operations via methods like `Read`, `Write`, `Create`, and `Wstat`
- Serve the tree over the network (typically TCP)

### Common usage

Most servers support `-addr`:

```bash
go run ./p/srv/examples/<name> -addr 127.0.0.1:5640
```

Then use one of the client examples in `p/clnt/examples/` (e.g. `ls`, `read`, `write`) to interact with it.

### `ufs` (Unix filesystem export)

- **Purpose**: export a real host directory tree over 9P (reference “real FS” server).
- **Backing storage**: OS filesystem.
- **Typical use**: interop testing, directory operations, metadata, etc.

Run:

```bash
go run ./p/srv/examples/ufs -addr 127.0.0.1:5640 -root .
```

### `ramfs` (in-memory ram filesystem)

- **Purpose**: demonstrate an in-memory file tree with dynamic `Create` and `Wstat` support.
- **Backing storage**: memory (file contents stored as blocks; sparse-ish by allowing empty blocks).
- **Interesting bits**:
  - `RFile.Create` dynamically attaches new children under a directory node.
  - `RFile.Read`/`Write` implement block-based storage.
  - `RFile.Wstat` demonstrates how rename, chmod, and truncate can be implemented in a synthetic server.

Run:

```bash
go run ./p/srv/examples/ramfs -addr 127.0.0.1:5640
```

### `timefs` (read-only synthetic time files)

- **Purpose**: demonstrate synthetic read-only files.
- **Structure**:
  - `/time`: returns `time.Now().String()` and supports offset reads.
  - `/inftime`: returns `time.Now().String()+"\n"` and **ignores** offset (effectively “infinite stream”).
- **Notes**:
  - Many tools will happily read `/time`.
  - Be careful reading `/inftime` with commands that read until EOF; it may not terminate.

Run:

```bash
go run ./p/srv/examples/timefs -addr 127.0.0.1:5640
```

### `clonefs` (Plan 9 style clone interface)

`clonefs` is a synthetic filesystem that mimics the classic Plan 9 pattern where reading a special
`/clone` file allocates a new numbered file.

- **Structure**:
  - `/clone` (read-only):
    - On the **first** read (offset 0), it allocates a new file under `/` with a numeric name (`"1"`, `"2"`, ...).
    - It returns that allocated name as the read result.
    - On subsequent reads (non-zero offset), it returns 0 bytes.
  - `/<n>` (read/write):
    - If nothing has been written yet, reading it yields a default string like `"<n> created on:<timestamp>"`.
    - Writes store data in memory; subsequent reads return the stored bytes.

This pattern is useful when you want “open a new session/endpoint” without requiring directory creates.

Run:

```bash
go run ./p/srv/examples/clonefs -addr 127.0.0.1:5640
```

Example interaction (using the Go client examples):

```bash
# Allocate a clone file name.
go run ./p/clnt/examples/read -addr 127.0.0.1:5640 /clone

# Suppose it prints "1". Write and read it back.
go run ./p/clnt/examples/write -addr 127.0.0.1:5640 /1
go run ./p/clnt/examples/read  -addr 127.0.0.1:5640 /1
```

### `tlsramfs` (TLS-wrapped ramfs)

- **Purpose**: show how to run the same synthetic tree (ramfs) over a TLS listener.
- **Transport**: TLS on top of TCP (no plaintext listener).
- **Client**: use `p/clnt/examples/tls` (or `clnt.MountConn` with a TLS connection).
- **Certificates**: uses an embedded test certificate/key (good for examples; not production).

Run:

```bash
go run ./p/srv/examples/tlsramfs -addr 127.0.0.1:5640
```

Then list a directory over TLS:

```bash
go run ./p/clnt/examples/tls -addr 127.0.0.1:5640 /
```

