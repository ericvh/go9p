package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func must(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %s: %v\n", msg, err)
		os.Exit(1)
	}
}

func mustEq[T comparable](got, want T, msg string) {
	if got != want {
		fmt.Fprintf(os.Stderr, "FAIL: %s: got=%v want=%v\n", msg, got, want)
		os.Exit(1)
	}
}

func readAll(path string) []byte {
	b, err := os.ReadFile(path)
	must(err, "read "+path)
	return b
}

func main() {
	// The initramfs mounts the host-exported 9p tag at /mnt/9p.
	root := os.Getenv("KERNEL9P_MOUNT")
	if root == "" {
		root = "/mnt/9p"
	}

	st, err := os.Stat(root)
	must(err, "stat mountpoint")
	if !st.IsDir() {
		must(fmt.Errorf("not a directory"), "mountpoint is dir")
	}

	work := filepath.Join(root, "kernel9p-smoke")
	_ = os.RemoveAll(work)
	must(os.MkdirAll(work, 0o777), "mkdir workdir")

	// Basic create/write/read.
	p := filepath.Join(work, "hello.txt")
	want := []byte("hello-from-kernel-9p\n")
	must(os.WriteFile(p, want, 0o666), "writefile")
	got := readAll(p)
	mustEq(string(got), string(want), "readback content")

	// Append semantics (open + write).
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o666)
	must(err, "open append")
	_, err = f.Write([]byte("append\n"))
	must(err, "append write")
	must(f.Close(), "close append")

	got2 := readAll(p)
	mustEq(string(got2), string(append(want, []byte("append\n")...)), "append readback")

	// Rename.
	p2 := filepath.Join(work, "renamed.txt")
	must(os.Rename(p, p2), "rename")
	_, err = os.Stat(p)
	if err == nil {
		must(fmt.Errorf("expected old path missing"), "old path missing after rename")
	}

	// Readdir.
	ents, err := os.ReadDir(work)
	must(err, "readdir")
	found := false
	for _, e := range ents {
		if e.Name() == "renamed.txt" {
			found = true
		}
	}
	if !found {
		must(fmt.Errorf("renamed.txt not found in readdir"), "readdir contains renamed.txt")
	}

	// Truncate via open.
	f2, err := os.OpenFile(p2, os.O_WRONLY|os.O_TRUNC, 0o666)
	must(err, "open trunc")
	_, err = f2.Write([]byte("x"))
	must(err, "write trunc")
	must(f2.Close(), "close trunc")
	got3 := readAll(p2)
	mustEq(string(got3), "x", "truncate result")

	// Large-ish streaming copy (tests read/write loops).
	src := filepath.Join(work, "src.bin")
	dst := filepath.Join(work, "dst.bin")
	buf := make([]byte, 256*1024)
	for i := range buf {
		buf[i] = byte(i)
	}
	must(os.WriteFile(src, buf, 0o666), "write src.bin")

	in, err := os.Open(src)
	must(err, "open src.bin")
	defer in.Close()
	out, err := os.Create(dst)
	must(err, "create dst.bin")
	_, err = io.Copy(out, in)
	must(err, "copy dst.bin")
	must(out.Close(), "close dst.bin")

	mustEq(len(readAll(dst)), len(buf), "copied size")

	// Cleanup (remove + rmdir).
	must(os.Remove(src), "remove src.bin")
	must(os.Remove(dst), "remove dst.bin")
	must(os.Remove(p2), "remove renamed.txt")
	must(os.RemoveAll(work), "remove workdir")

	fmt.Println("PASS: kernel 9p client smoke test")
}

