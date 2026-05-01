package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCloneShell_ChdirToSessionDir(t *testing.T) {
	root := t.TempDir()

	// Simulate a clonefs-like layout under root:
	//   /x/clone -> "123\n"
	//   /x/123/ctl
	if err := os.MkdirAll(filepath.Join(root, "x", "123"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "x", "clone"), []byte("123\n"), 0o666); err != nil {
		t.Fatalf("write clone: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "x", "123", "ctl"), []byte{}, 0o666); err != nil {
		t.Fatalf("write ctl: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	// Run a trivial command (the function itself does the chdir).
	code := runCloneShell(root, "/x/clone", "/bin/sh", "exit 0", "", "", "")
	if code != 0 {
		t.Fatalf("exit code=%d", code)
	}

	got, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	want := filepath.Join(root, "x", "123")
	got2, _ := filepath.EvalSymlinks(got)
	want2, _ := filepath.EvalSymlinks(want)
	if got2 != want2 {
		t.Fatalf("cwd=%q want %q", got2, want2)
	}
}

func TestCloneShell_ClonefsTemplates(t *testing.T) {
	root := t.TempDir()

	// clonefs-like: clone creates a file {id} (no session dir, no ctl file).
	if err := os.MkdirAll(filepath.Join(root, "x"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "x", "clone"), []byte("7\n"), 0o666); err != nil {
		t.Fatalf("write clone: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "x", "7"), []byte{}, 0o666); err != nil {
		t.Fatalf("write cloned file: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	code := runCloneShell(
		root,
		"/x/clone",
		"/bin/sh",
		"exit 0",
		"{clone_dir}",      // session dir is the clone's parent directory
		"{clone_dir}/{id}", // "ctl" to hold open is the cloned file itself
		"{clone_dir}",      // chdir to the directory
	)
	if code != 0 {
		t.Fatalf("exit code=%d", code)
	}
}

