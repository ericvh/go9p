package kfs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ReadAll(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func WriteAll(path string, data []byte) error {
	// Avoid O_TRUNC semantics: many synthetic 9P files don't implement wstat/truncate,
	// and the Linux kernel client may translate O_TRUNC into a setattr that fails.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		// Best-effort fallback for ordinary filesystems.
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o666)
		if err != nil {
			return err
		}
	}
	defer f.Close()

	for len(data) > 0 {
		n, werr := f.Write(data)
		if werr != nil {
			return werr
		}
		data = data[n:]
	}
	return nil
}

func Join(root string, elems ...string) string {
	all := append([]string{root}, elems...)
	return filepath.Clean(filepath.Join(all...))
}

func CloneID(clonePath string) (string, error) {
	b, err := os.ReadFile(clonePath)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", fmt.Errorf("clone returned empty id")
	}
	return id, nil
}

func MustTrailingNL(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	if b[len(b)-1] == '\n' {
		return b
	}
	return append(append([]byte(nil), b...), '\n')
}

func CopyBothWays(a io.ReadWriteCloser, b io.ReadWriteCloser) error {
	defer a.Close()
	defer b.Close()

	errc := make(chan error, 2)
	go func() {
		_, err := io.Copy(a, b)
		errc <- err
	}()
	go func() {
		_, err := io.Copy(b, a)
		errc <- err
	}()

	// Return first non-nil error; ignore EOF-ish behavior.
	var first error
	for i := 0; i < 2; i++ {
		if err := <-errc; err != nil && first == nil {
			first = err
		}
	}
	return first
}

func ReadTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func SplitLines(b []byte) []string {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\n")
}

