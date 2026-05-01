package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
)

func dief(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
}

func expandUnderRoot(root, p string) string {
	if strings.HasPrefix(p, "/") {
		return filepath.Clean(filepath.Join(root, p))
	}
	return filepath.Clean(filepath.Join(root, p))
}

func applyCloneTemplate(tmpl, root, clonePath, cloneDir, id string) string {
	// Template may use:
	//   {root}      - cleaned root
	//   {clone}     - full clone path (under root)
	//   {clone_dir} - directory containing clone
	//   {id}        - id read from clone
	out := tmpl
	out = strings.ReplaceAll(out, "{root}", root)
	out = strings.ReplaceAll(out, "{clone}", clonePath)
	out = strings.ReplaceAll(out, "{clone_dir}", cloneDir)
	out = strings.ReplaceAll(out, "{id}", id)
	return out
}

func runCloneShell(root, clonePath, shell, cmd, sessionTmpl, ctlTmpl, chdirTmpl string) int {
	if clonePath == "" {
		fmt.Fprintf(os.Stderr, "error: missing -clone\n")
		return 2
	}
	root = filepath.Clean(root)
	clonePath = expandUnderRoot(root, clonePath)
	cloneDir := filepath.Dir(clonePath)

	clonef, err := os.Open(clonePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open clone: %v\n", err)
		return 2
	}
	defer clonef.Close()

	idLine, err := bufio.NewReader(clonef).ReadString('\n')
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "error: read clone: %v\n", err)
		return 2
	}
	id := strings.TrimSpace(idLine)
	if id == "" {
		fmt.Fprintf(os.Stderr, "error: clone returned empty id\n")
		return 2
	}

	if sessionTmpl == "" {
		sessionTmpl = "{clone_dir}/{id}"
	}
	if ctlTmpl == "" {
		ctlTmpl = "{session}/ctl"
	}
	if chdirTmpl == "" {
		chdirTmpl = "{session}"
	}
	sessDir := applyCloneTemplate(sessionTmpl, root, clonePath, cloneDir, id)
	// Allow ctl/chdir templates to refer to computed session via {session}.
	ctlPath := strings.ReplaceAll(ctlTmpl, "{session}", sessDir)
	ctlPath = applyCloneTemplate(ctlPath, root, clonePath, cloneDir, id)
	chdirPath := strings.ReplaceAll(chdirTmpl, "{session}", sessDir)
	chdirPath = applyCloneTemplate(chdirPath, root, clonePath, cloneDir, id)

	// Hold ctl open for the lifetime of the subshell; servers GC on last ctl close.
	//
	// Prefer O_RDWR since ctl is typically both readable (status) and writable
	// (commands). Fall back for kernel mount / synthetic permission quirks.
	ctlf, err := os.OpenFile(ctlPath, os.O_RDWR, 0)
	if err != nil {
		ctlf, err = os.OpenFile(ctlPath, os.O_WRONLY, 0)
	}
	if err != nil {
		ctlf, err = os.OpenFile(ctlPath, os.O_RDONLY, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: open ctl: %v\n", err)
			return 2
		}
	}
	defer ctlf.Close()

	if err := os.Chdir(chdirPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: chdir session: %v\n", err)
		return 2
	}

	if shell == "" {
		shell = strings.TrimSpace(os.Getenv("SHELL"))
	}
	if shell == "" {
		shell = "/bin/sh"
	}

	args := []string{}
	if cmd != "" {
		args = append(args, "-c", cmd)
	} else {
		// Interactive by default.
		args = append(args, "-i")
	}
	c := exec.Command(shell, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = os.Environ()
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "error: exec shell: %v\n", err)
		return 2
	}
	return 0
}

type session struct {
	root string
	vars map[string]string
	cwd  string
}

func (s *session) expand(p string) string {
	if strings.HasPrefix(p, "$") && len(p) > 1 {
		if v, ok := s.vars[p[1:]]; ok {
			p = v
		}
	}
	if strings.HasPrefix(p, "/") {
		return filepath.Clean(filepath.Join(s.root, p))
	}
	// relative to cwd (which is always rooted at s.root).
	return filepath.Clean(filepath.Join(s.cwd, p))
}

func (s *session) cmd(line string) (quit bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "quit", "exit":
		return true
	case "help":
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  help\n")
		fmt.Fprintf(os.Stderr, "  exit | quit\n")
		fmt.Fprintf(os.Stderr, "  pwd\n")
		fmt.Fprintf(os.Stderr, "  cd <dir>\n")
		fmt.Fprintf(os.Stderr, "  set <name> <value>\n")
		fmt.Fprintf(os.Stderr, "  vars\n")
		fmt.Fprintf(os.Stderr, "  cat <path>\n")
		fmt.Fprintf(os.Stderr, "  write <path> <text...>\n")
		fmt.Fprintf(os.Stderr, "  put <path>    (reads stdin until EOF)\n")
		fmt.Fprintf(os.Stderr, "  clone <path>  (reads clone file; stores as $last)\n")
		fmt.Fprintf(os.Stderr, "\nNotes:\n")
		fmt.Fprintf(os.Stderr, "  - All paths are resolved under -root.\n")
		fmt.Fprintf(os.Stderr, "  - $name expands to a stored variable (useful for clone ids).\n")
		return false
	case "pwd":
		rel, _ := filepath.Rel(s.root, s.cwd)
		if rel == "." {
			fmt.Println("/")
		} else {
			fmt.Println("/" + filepath.ToSlash(rel))
		}
		return false
	case "cd":
		if len(fields) != 2 {
			fmt.Fprintf(os.Stderr, "usage: cd <dir>\n")
			return false
		}
		p := fields[1]
		if strings.HasPrefix(p, "/") {
			s.cwd = filepath.Clean(filepath.Join(s.root, p))
		} else {
			s.cwd = filepath.Clean(filepath.Join(s.cwd, p))
		}
		return false
	case "set":
		if len(fields) < 3 {
			fmt.Fprintf(os.Stderr, "usage: set <name> <value>\n")
			return false
		}
		name := fields[1]
		val := strings.Join(fields[2:], " ")
		s.vars[name] = val
		return false
	case "vars":
		for k, v := range s.vars {
			fmt.Printf("%s=%s\n", k, v)
		}
		return false
	case "cat":
		if len(fields) != 2 {
			fmt.Fprintf(os.Stderr, "usage: cat <path>\n")
			return false
		}
		p := s.expand(fields[1])
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cat %s: %v\n", fields[1], err)
			return false
		}
		_, _ = os.Stdout.Write(b)
		return false
	case "write":
		if len(fields) < 3 {
			fmt.Fprintf(os.Stderr, "usage: write <path> <text...>\n")
			return false
		}
		p := s.expand(fields[1])
		data := strings.Join(fields[2:], " ")
		if !strings.HasSuffix(data, "\n") {
			data += "\n"
		}
		if err := os.WriteFile(p, []byte(data), 0o666); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", fields[1], err)
		}
		return false
	case "put":
		if len(fields) != 2 {
			fmt.Fprintf(os.Stderr, "usage: put <path>\n")
			return false
		}
		p := s.expand(fields[1])
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "put stdin: %v\n", err)
			return false
		}
		if err := os.WriteFile(p, b, 0o666); err != nil {
			fmt.Fprintf(os.Stderr, "put %s: %v\n", fields[1], err)
		}
		return false
	case "clone":
		if len(fields) != 2 {
			fmt.Fprintf(os.Stderr, "usage: clone <path>\n")
			return false
		}
		p := s.expand(fields[1])
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "clone %s: %v\n", fields[1], err)
			return false
		}
		id := strings.TrimSpace(string(b))
		if id == "" {
			fmt.Fprintf(os.Stderr, "clone %s: empty id\n", fields[1])
			return false
		}
		s.vars["last"] = id
		fmt.Println(id)
		return false
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (try: help)\n", fields[0])
		return false
	}
}

func main() {
	// Subcommand: clone-shell
	// Usage: go9p-ksession clone-shell -root /mnt/go9p -clone /netfs/net/tcp/clone [-shell /bin/bash] [-cmd '...']
	if len(os.Args) > 1 && os.Args[1] == "clone-shell" {
		fs := flag.NewFlagSet(os.Args[0]+" clone-shell", flag.ExitOnError)
		fs.SetOutput(os.Stderr)
		var root string
		var clonePath string
		var shell string
		var cmd string
		var sessionTmpl string
		var ctlTmpl string
		var chdirTmpl string
		root = strings.TrimSpace(os.Getenv("GO9P_MOUNT"))
		if root == "" {
			root = "/mnt/go9p"
		}
		fs.StringVar(&root, "root", root, "path to mounted root (e.g. /mnt/go9p)")
		fs.StringVar(&clonePath, "clone", "", "clone file path under -root (e.g. /netfs/net/tcp/clone)")
		fs.StringVar(&shell, "shell", "", "shell path (default: $SHELL, fallback /bin/sh)")
		fs.StringVar(&cmd, "cmd", "", "run a command via shell -c instead of interactive subshell (useful for tests)")
		fs.StringVar(&sessionTmpl, "session", "", "session dir template (default: {clone_dir}/{id})")
		fs.StringVar(&ctlTmpl, "ctl", "", "ctl path template (default: {session}/ctl). Use e.g. {clone_dir}/{id} for clonefs.")
		fs.StringVar(&chdirTmpl, "chdir", "", "chdir template (default: {session}). Use e.g. {clone_dir} for clonefs.")
		_ = fs.Parse(os.Args[2:])
		os.Exit(runCloneShell(root, clonePath, shell, cmd, sessionTmpl, ctlTmpl, chdirTmpl))
	}

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	var root string
	var prompt string
	root = strings.TrimSpace(os.Getenv("GO9P_MOUNT"))
	if root == "" {
		root = "/mnt/go9p"
	}
	fs.StringVar(&root, "root", root, "path to mounted root (e.g. /mnt/go9p)")
	fs.StringVar(&prompt, "prompt", "go9p(k)> ", "prompt")
	_ = fs.Parse(os.Args[1:])
	if root == "" {
		dief("missing -root")
	}
	root = filepath.Clean(root)

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt)
	go func() {
		<-ch
		cancel()
		<-ch
		os.Exit(2)
	}()

	s := &session{
		root: root,
		vars: map[string]string{},
		cwd:  root,
	}

	in := bufio.NewScanner(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		fmt.Fprint(os.Stderr, prompt)
		if !in.Scan() {
			return
		}
		if s.cmd(in.Text()) {
			return
		}
	}
}

