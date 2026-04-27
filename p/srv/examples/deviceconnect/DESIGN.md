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

## Backend contract

Internally, the filesystem talks to a backend interface:

- list devices
- get per-device metadata/status and function inventory
- invoke a function

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

