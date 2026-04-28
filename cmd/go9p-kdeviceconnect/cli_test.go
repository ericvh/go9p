package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKDeviceConnectCall_FakeTree(t *testing.T) {
	root := t.TempDir()

	// Build a minimal fake deviceconnect tree that matches the CLI expectations.
	devRoot := filepath.Join(root, "devices")
	mustMkdirAll(t, filepath.Join(devRoot, "by-id", "d1", "functions", "echo", "1"))
	mustWrite(t, filepath.Join(devRoot, "discover"), "d1\n")
	mustWrite(t, filepath.Join(devRoot, "by-id", "d1", "meta"), "meta\n")
	mustWrite(t, filepath.Join(devRoot, "by-id", "d1", "status"), "ok\n")
	mustWrite(t, filepath.Join(devRoot, "by-id", "d1", "functions", "echo", "clone"), "1\n")
	mustWrite(t, filepath.Join(devRoot, "by-id", "d1", "functions", "echo", "1", "ctl"), "")
	mustWrite(t, filepath.Join(devRoot, "by-id", "d1", "functions", "echo", "1", "error"), "")
	mustWrite(t, filepath.Join(devRoot, "by-id", "d1", "functions", "echo", "1", "stream"), "resp\n")

	// Emulate a stream result (the CLI reads stream file when -stream is set).
	mustWrite(t, filepath.Join(devRoot, "by-id", "d1", "functions", "echo", "1", "data"), "")

	cmd := exec.Command("go", "run", ".", "-root", devRoot, "call", "-id", "d1", "-fn", "echo", "-payload", "req", "-stream")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cmd.Dir = wd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, string(out))
	}
	if strings.TrimSpace(string(out)) != "resp" {
		t.Fatalf("out=%q want %q", string(out), "resp\n")
	}
}

func mustMkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o777); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(p, []byte(s), 0o666); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

