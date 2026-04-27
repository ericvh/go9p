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
- Make invocation a **filesystem protocol step**:
  - `write` to a file triggers a function call
  - `read` returns the last result (or last error)
- Keep the interface **toolable** (`ls`, `cat`, `echo`) and easy to test using the `go9p` userspace client.

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
            invoke
            result
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

- identical semantics to device-level `events/*`, but scoped to a particular value stream.

### `devices/by-id/<device-id>/functions/<fn>/about`

- **read-only**: short description of the function.

### `devices/by-id/<device-id>/functions/<fn>/schema`

- **read-only**: a human-readable “schema” for invocation payloads.

This example keeps schemas textual; production systems may want JSON Schema or protobuf descriptors.

### `devices/by-id/<device-id>/functions/<fn>/invoke`

- **write**: invokes the function. The write payload is passed as the function input.
- **read**: returns the last invocation result (or the last error if the most recent invocation failed).

This is the key synthetic-filesystem move: **invocation is a write**.

### `devices/by-id/<device-id>/functions/<fn>/result`

- **read-only**: synonym for “last successful result” (does not surface last error).

This file exists so callers can distinguish “no result yet” vs “last call errored”.

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
- invoke a function
 - subscribe to per-device events
 - optionally subscribe to per-value events

In this repo, tests use a **fake backend** and interact with the example exclusively through the **Go 9P client** (`p/clnt`).

## Why no `clone` yet?

Many synthetic filesystems use `clone` + per-instance directories for sessions, leases, or subscriptions.
This example keeps invocation stateless (idempotent “request → last result”) because it is meant as a minimal baseline.

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

