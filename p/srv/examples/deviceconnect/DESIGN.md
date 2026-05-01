# Device Connect synthetic filesystem (`deviceconnect`)

This example implements a **synthetic filesystem** that exposes a subset of the core Device Connect model from `deviceconnect.dev`:

- **devices** exist in a shared mesh and are discoverable
- each device exposes **functions** (Device Connect `@rpc` style)
- agents / users can **invoke** functions and inspect **status / metadata**

The goal is not to faithfully reimplement Device Connect’s transport (Zenoh/NATS/MQTT) inside `go9p`, but to demonstrate how to project Device Connect’s *object model* into a 9P filesystem using the conventions described in:

- `synthetic-filesystems.md` (control/data separation, object-centered trees, `clone`/`ctl` patterns)
- `agentic-synthetic-filesystems.md` (hierarchy as progressive discovery, capability surfaces, event files)

## Goals

- Provide a **hierarchical namespace** that makes device→function relationships obvious.
- Make function calls a **filesystem protocol step** using Plan 9 conventions (`clone`, `ctl`, `data`).
- Keep the interface **toolable** (`ls`, `cat`, `echo`) and easy to test using the `go9p` userspace client.

For a quick tour of the exposed paths, also see `p/srv/examples/README.md`.

## Non-goals (for this example)

- Implement Device Connect’s full server stack, commissioning, mTLS/JWT auth, ACL enforcement, or audit logging.
- Implement high-rate streaming data planes (video, telemetry). This example focuses on low-rate control/status.

## Filesystem hierarchy

The filesystem root contains a `devices/` tree.

```text
/
  devices/
    discover
    by-id/
      <device-id>/
        meta
        status
        events/
          replay
          stream
        values/
          <value-name>/
            value
            events/
              replay
              stream
        functions/
          <function-name>/
            about
            schema
            clone
            <call-id>/
              ctl
              data
              error
              stream
```

### `devices/discover`

- **read**: returns a newline-delimited list of known devices (`<id>\t<type>`).
- **write**: accepts `refresh\n` (or any content) to trigger a refresh from the backend.

This is intentionally simple: it’s a discoverability “index” and a manual refresh hook.

### `devices/by-id/<device-id>/meta`

- **read-only**: stable metadata about the device (human-readable text).

### `devices/by-id/<device-id>/status`

- **read-only**: status / health / availability (human-readable text).

## Values (device-level readings)

Many devices expose “simple readings” (temperature, humidity, battery %, etc.) that are conceptually **values**
rather than “functions you invoke”. Device Connect’s examples show `@rpc` and `@emit`; this example adds a values
projection so a consumer can do:

- `cat .../values/temp/value`
- optionally tail or page through `.../values/temp/events/replay`

This keeps *pull* (snapshot value) distinct from *push* (events) and avoids inventing a “get_*” function purely for reads.

### `devices/by-id/<device-id>/values/<name>/value`

- **read-only**: a snapshot of the current value.
- value is textual in this example (typically a single line).

### `devices/by-id/<device-id>/values/<name>/events/{replay,stream}`

- identical semantics to device-level `events/`*, but scoped to a particular value stream.

### `devices/by-id/<device-id>/functions/<fn>/about`

- **read-only**: short description of the function.

### `devices/by-id/<device-id>/functions/<fn>/schema`

- **read-only**: a human-readable “schema” for invocation payloads.

This example keeps schemas textual; production systems may want JSON Schema or protobuf descriptors.

### `devices/by-id/<device-id>/functions/<fn>/clone`

- **read-only**: allocates a new call instance directory (returns the numeric id).

### `devices/by-id/<device-id>/functions/<fn>/<id>/ctl`

- **write-only**: control surface for the call instance.

Supported commands:

- `call`: invoke the function using the current request bytes written to `data`
- `stream`: invoke the function in streaming mode (if supported)
- `reset`: clear request/response/error buffers

On failure, `ctl` returns a 9P error (Rerror). This is the “standard” mechanism.

## Call instance lifecycle

Reading `clone` allocates a numbered subdirectory (e.g. `functions/echo/7/`).
That directory's lifetime is tied to the **9P connection** that allocated
it: when the connection drops — clean unmount, client crash, or network
loss — the example's server wrapper (`dcSrv`) reaps every call instance
attached to that connection via the framework's `ConnClosed` hook.

This mirrors the canonical Plan 9 idiom: `/net/tcp` connections, `/srv`
posts, and per-process namespaces all evaporate with the connection that
created them. It also means:

- The CLI does not need to write any explicit "close" command after a call.
- A client that crashes mid-call doesn't leak — the connection still drops
  and cleanup still runs.
- Long-lived servers do not accumulate dead call directories.

### `devices/by-id/<device-id>/functions/<fn>/<id>/data`

- **read/write**:
  - write request bytes (payload) before calling
  - read response bytes after calling

### `devices/by-id/<device-id>/functions/<fn>/<id>/error`

- **read-only**: stores the last error string for the call instance.

This exists because Linux kernel mounts often collapse 9P errors into generic errno values on write;
reading `error` preserves the detailed message even when the caller can’t see the original 9P Rerror.

### `devices/by-id/<device-id>/functions/<fn>/<id>/stream`

- **read-only**: a bounded buffer of streamed output chunks produced by `ctl stream`.

Notes:

- Streaming support is **function-dependent**; if the backend doesn’t support streaming for a function,
  `ctl stream` returns a 9P error and records details in `error`.
- This example keeps `stream` non-blocking and bounded (similar to the per-device event logs) to avoid
  hanging basic tooling and unit tests. Production systems may prefer a blocking stream or an external
  transport handle.

## Events

Device Connect exposes event-style outputs via `@emit` (and `subscribe()` on the agent side).
This example surfaces that idea per-device:

### `devices/by-id/<device-id>/events/replay`

- **read-only**: returns a bounded, newline-delimited log of recent events for the device.
- read supports offsets, so callers can page through a large log using repeated reads with increasing offsets.

### `devices/by-id/<device-id>/events/stream`

- **read-only**: returns the same underlying log as `replay`.

In a production-grade server, `stream` would typically **block** waiting for new events when a client reads at EOF,
or expose an indirection handle to a native stream transport. This example keeps `stream` non-blocking so tests and
simple tooling do not hang.

## Backend contract

Internally, the filesystem talks to a backend interface:

- list devices
- get per-device metadata/status and function inventory
- read per-device values (snapshot reads)
- invoke a function (backend call that `ctl` triggers)
- subscribe to per-device events
- optionally subscribe to per-value events

In this repo, tests use a **fake backend** and interact with the example exclusively through the **Go 9P client** (`p/clnt`).

## Why `clone` for functions?

We use `clone` for function calls so each call has a **stable per-invocation directory** that can hold:

- the request bytes (written to `data`)
- the response bytes (read from `data`)
- the last error string (read from `error`)

This mirrors classic Plan 9 patterns (`/net/tcp/clone` → `<id>/ctl` + `<id>/data`) and avoids overloading a single file
with “write triggers call” and “read returns result” semantics.

## Possible next expansion: sessions

Many synthetic filesystems also use `clone` at a higher level to allocate sessions, leases, or subscriptions.
An obvious next expansion is:

```text
/sessions/
  clone
  <sid>/
    ctl
    devices/...
    events/{stream,replay}
```

where `clone` allocates a session and `events/stream` aggregates device events relevant to that session.

### Note on canonical mounts / `/srv`-style indirection (future)

For convenience, it’s attractive to standardize on **canonical ports** (for the userspace 9P server) and
**canonical mount points** (for the Linux kernel 9p client), so basic tooling works without extra flags.

In a multi-agent setting, it may also be useful to add a Plan 9-like `/srv` equivalent (or a `sessions/` namespace)
that lets each agent attach to a distinct view of the device mesh (per-agent attach names, per-session filters, etc.).
This design intentionally keeps the first version simple (single shared `devices/` tree) and leaves `/srv`-style
indirection as a potential follow-on.
