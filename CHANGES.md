# Changes

This file tracks notable changes on the `ericvh/go9p` fork branches (not upstream release notes).

## Unreleased

### Go / tooling

- Add `go.mod` and enable module-based builds (`module github.com/lionkov/go9p`)
- Target modern Go (`go 1.26.0`) and set `toolchain go1.26.0`

### Tests / CI hygiene

- Fix `go test ./...` failures on modern Go:
  - Resolve `go vet` “non-constant format string” warnings
  - Make Unix socket-based tests portable by using a temp socket path instead of `net.Listen("unix", "")`
  - Avoid flaky failures when shutting down the listener (ignore expected “use of closed network connection” on close)
- Add Docker-based Linux test runner (`Dockerfile`) with `test` and `race` targets.
- Add GitHub Actions CI that runs Docker-based tests on every push/PR.
- Add end-to-end client/server integration tests:
  - UFS-backed e2e test in `p/clnt`
  - Fsrv synthetic-tree e2e test in `p/srv`
- Add QEMU-based Linux kernel 9p client smoke test (`Dockerfile.kernel9p-qemu`) and run it in CI on amd64/arm64.
- Add `HERZOG.md` as an AI-generated stylistic mirror of `README.md`, with CI enforcement to keep headings in sync.

