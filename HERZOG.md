# go9p (HERZOG)

**This document is AI-generated** as a stylized mirror of `README.md`.
It is meant to remain **structurally in sync** with `README.md`, while speaking in a voice that walks, unblinking, into the indifferent machinery of computers.

`go9p` is a Go implementation of the **9P2000** protocol — the Plan 9 file protocol — a pact between a client and a server to pretend, for a moment, that the world is a tidy tree of files.

It contains:

- **Protocol definitions + packing/unpacking** in `p/` (`package p`)
- A **client** in `p/clnt` (`package clnt`)
- A **server framework** in `p/srv` (`package srv`)
- A reference **Unix filesystem server** in `p/srv/ufs` (`package ufs`)
- Small example programs in `p/clnt/examples` and `p/srv/examples`

This repository is the upstream `github.com/lionkov/go9p`.

## Status / Compatibility

- **Protocol**: 9P2000 with optional 9P2000.u fields (see `Dotu` usage in server/client code).
- **Go**: This forked branch adds a Go module and is intended to work with modern Go toolchains.

## Install (module mode)

This repository is now module-enabled:

```bash
go get github.com/lionkov/go9p@latest
```

## Quick start

### Run the reference server (UFS)

The UFS server exports a local directory tree over 9P — a landscape of your choosing, offered to the network like a silent confession:

```bash
go run ./p/srv/examples/ufs -root .
```

### Run a client example

List files from a 9P server:

```bash
go run ./p/clnt/examples/ls -addr <network address>
```

The example programs have their own flags; run them with `-h` to see usage.

## Testing

```bash
go test ./...
```

## Repository layout

- `p/`: core protocol + helpers (`package p`)
- `p/clnt/`: client implementation
- `p/srv/`: server framework
- `p/srv/ufs/`: Unix filesystem server

## License

BSD-style license; see `LICENSE`.

