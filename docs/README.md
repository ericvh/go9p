## CLI documentation

This directory contains markdown “man page”-style documentation for the higher-level CLI tools under `cmd/`.

There are two families:

- **go9p-client CLIs**: speak 9P directly using `p/clnt` (work on any OS supported by Go).
- **kernel-mounted CLIs**: operate on ordinary file paths and are intended to be used **against a Linux kernel 9p mount**
  (e.g. `mount -t 9p ...`).

Start here:

- `cli-netfs.md`
- `cli-deviceconnect.md`
- `cli-session.md`

