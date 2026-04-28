package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lionkov/go9p/cmd/internal/cli9p"
	"github.com/lionkov/go9p/p/clnt"
)

func dief(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
}

type session struct {
	c    *clnt.Clnt
	vars map[string]string
	cwd  string
}

func (s *session) expand(p string) string {
	// $name expansion (whole-token).
	if strings.HasPrefix(p, "$") && len(p) > 1 {
		if v, ok := s.vars[p[1:]]; ok {
			p = v
		}
	}
	if strings.HasPrefix(p, "/") {
		return p
	}
	if s.cwd == "" || s.cwd == "/" {
		return "/" + strings.TrimPrefix(p, "/")
	}
	return strings.TrimRight(s.cwd, "/") + "/" + strings.TrimPrefix(p, "/")
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
		fmt.Fprintf(os.Stderr, "  - Relative paths are resolved against a session cwd.\n")
		fmt.Fprintf(os.Stderr, "  - $name expands to a stored variable (useful for clone ids).\n")
		return false
	case "pwd":
		if s.cwd == "" {
			fmt.Println("/")
		} else {
			fmt.Println(s.cwd)
		}
		return false
	case "cd":
		if len(fields) != 2 {
			fmt.Fprintf(os.Stderr, "usage: cd <dir>\n")
			return false
		}
		s.cwd = s.expand(fields[1])
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
		b, err := cli9p.ReadAll(s.c, p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cat %s: %v\n", p, err)
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
		if err := cli9p.WriteAll(s.c, p, []byte(data), true); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", p, err)
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
		if err := cli9p.WriteAll(s.c, p, b, true); err != nil {
			fmt.Fprintf(os.Stderr, "put %s: %v\n", p, err)
		}
		return false
	case "clone":
		if len(fields) != 2 {
			fmt.Fprintf(os.Stderr, "usage: clone <path>\n")
			return false
		}
		p := s.expand(fields[1])
		b, err := cli9p.ReadAll(s.c, p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "clone %s: %v\n", p, err)
			return false
		}
		id := cli9p.TrimLine(b)
		if id == "" {
			fmt.Fprintf(os.Stderr, "clone %s: empty id\n", p)
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
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	var cf cli9p.ConnFlags
	cf.Register(fs)
	var prompt string
	fs.StringVar(&prompt, "prompt", "go9p> ", "prompt")
	_ = fs.Parse(os.Args[1:])

	c, err := cf.Mount()
	if err != nil {
		dief("mount: %v", err)
	}
	defer c.Unmount()

	ctx, cancel := context.WithCancel(context.Background())
	cli9p.TrapInterrupt(cancel)

	s := &session{
		c:    c,
		vars: map[string]string{},
		cwd:  "/",
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

