## Session-style CLI (clone-friendly)

Some synthetic filesystems use **clone** semantics (`clonefs`, `netfs` conversations, `deviceconnect` function calls).

This repo provides two REPL-style session tools:

- **go9p-client**: `cmd/go9p-session` (talks 9P directly)
- **kernel-mounted**: `cmd/go9p-ksession` (talks to a mounted filesystem tree)

### go9p-client session

```bash
go run ./cmd/go9p-session
```

### kernel-mounted session

```bash
go run ./cmd/go9p-ksession
```

### Common commands

- `clone <path>`: reads a clone file and stores the returned id into `$last`
- `cat <path>`: read a file
- `write <path> <text...>`: truncate + write a line (adds trailing `\n` if missing)
- `put <path>`: truncate + write bytes from stdin
- `cd`, `pwd`, `set`, `vars`

Example (clonefs):

```text
clone /clone
cat $last
write $last hello
cat $last
```

