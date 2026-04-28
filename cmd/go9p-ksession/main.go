package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
)

func dief(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
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
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	var root string
	var prompt string
	fs.StringVar(&root, "root", "", "path to mounted root (e.g. /mnt/9p)")
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

