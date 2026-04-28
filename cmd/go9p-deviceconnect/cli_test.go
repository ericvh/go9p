package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGo9pDeviceConnect_Discover(t *testing.T) {
	// Start the example server as an external process (package is main).
	addr := "127.0.0.1:5679"
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, "..", ".."))
	srv := exec.Command("go", "run", "./p/srv/examples/deviceconnect", "-addr", addr)
	srv.Dir = repoRoot
	srv.Stdout = io.Discard
	srv.Stderr = io.Discard
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })

	// Wait for listener to be ready (go run compile can take a moment).
	deadline := time.Now().Add(8 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 150*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not become ready: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	var out bytes.Buffer
	var errb bytes.Buffer
	code := cmdDiscover(&out, &errb, []string{"-addr", addr})
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "robot-001") || !strings.Contains(s, "sensor-001") {
		t.Fatalf("unexpected discover output: %q", s)
	}
}

