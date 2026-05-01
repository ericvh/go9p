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

### kernel-mounted clone subshell

If your filesystem uses clone semantics that create a per-session directory with a `ctl` file,
you can launch a subshell that **keeps `ctl` open for the lifetime of the shell**.
When the subshell exits, `ctl` is closed and the server can garbage collect the session directory.

```bash
# Example: netfs conversation subshell
go run ./cmd/go9p-ksession clone-shell -root /mnt/go9p -clone /netfs/net/tcp/clone
```

For filesystems where `clone` creates a file directly (like the `clonefs` example),
you can adapt paths with templates:

```bash
go run ./cmd/go9p-ksession clone-shell -root /mnt/go9p -clone /clone \
  -session '{clone_dir}' -ctl '{clone_dir}/{id}' -chdir '{clone_dir}'
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

