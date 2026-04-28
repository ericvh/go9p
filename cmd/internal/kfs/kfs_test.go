package kfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCloneID_TrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clone")
	if err := os.WriteFile(p, []byte("  123 \n"), 0o666); err != nil {
		t.Fatalf("write clone: %v", err)
	}
	id, err := CloneID(p)
	if err != nil {
		t.Fatalf("CloneID: %v", err)
	}
	if id != "123" {
		t.Fatalf("id=%q want %q", id, "123")
	}
}

func TestCloneID_EmptyIsError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clone")
	if err := os.WriteFile(p, []byte("\n"), 0o666); err != nil {
		t.Fatalf("write clone: %v", err)
	}
	_, err := CloneID(p)
	if err == nil {
		t.Fatalf("expected error")
	}
}

