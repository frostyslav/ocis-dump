package mpkview

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func writeMPK(t *testing.T, path string, v map[string][]byte) {
	t.Helper()
	b, err := msgpack.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFormatMapSortedKeys(t *testing.T) {
	got := FormatMap(map[string]any{"b": []byte("2"), "a": "1", "n": 42}, "")
	want := "{\n  b\"a\": b\"1\",\n  b\"b\": b\"2\",\n  b\"n\": 42,\n}"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunSingleFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.mpk")
	writeMPK(t, p, map[string][]byte{"hello": []byte("world")})
	var buf bytes.Buffer
	if err := Run(p, false, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `b"hello": b"world"`) {
		t.Errorf("unexpected output: %s", buf.String())
	}
}

func TestRunMissingFile(t *testing.T) {
	var buf bytes.Buffer
	if err := Run(filepath.Join(t.TempDir(), "nope.mpk"), false, &buf); err == nil {
		t.Error("expected error")
	}
}

func TestRunInvalidFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.mpk")
	if err := os.WriteFile(p, []byte("not msgpack"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Run(p, false, &buf); err == nil || !strings.Contains(err.Error(), "unpack failed") {
		t.Errorf("got %v", err)
	}
}

func TestRunSearchDirectory(t *testing.T) {
	d := t.TempDir()
	writeMPK(t, filepath.Join(d, "a.mpk"), map[string][]byte{"name": []byte("a")})
	writeMPK(t, filepath.Join(d, "sub", "b.mpk"), map[string][]byte{"name": []byte("b")})
	var buf bytes.Buffer
	if err := Run(d, true, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"a.mpk", "b.mpk", `b"name": b"a"`, `b"name": b"b"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunSearchEmptyDirectory(t *testing.T) {
	var buf bytes.Buffer
	if err := Run(t.TempDir(), true, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "{\n}" {
		t.Errorf("got %q", buf.String())
	}
}

type failingWriter struct{ okWrites int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.okWrites <= 0 {
		return 0, errors.New("write failed")
	}
	w.okWrites--
	return len(p), nil
}

func TestRunSearchWithCorruptFile(t *testing.T) {
	d := t.TempDir()
	writeMPK(t, filepath.Join(d, "a.mpk"), map[string][]byte{"k": []byte("v")})
	if err := os.WriteFile(filepath.Join(d, "bad.mpk"), []byte("not msgpack"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Run(d, true, &buf); err == nil || !strings.Contains(err.Error(), "unpack failed") {
		t.Errorf("got %v", err)
	}
}

func TestRunPathErrors(t *testing.T) {
	d := t.TempDir()
	var buf bytes.Buffer
	if err := Run(filepath.Join(d, "missing"), false, &buf); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing: %v", err)
	}
	f := filepath.Join(d, "a.mpk")
	writeMPK(t, f, map[string][]byte{"k": []byte("v")})
	if err := Run(f, true, &buf); err == nil || !strings.Contains(err.Error(), "asked to search directory") {
		t.Errorf("search on file: %v", err)
	}
}

func TestRunPropagatesWriteErrors(t *testing.T) {
	d := t.TempDir()
	writeMPK(t, filepath.Join(d, "a.mpk"), map[string][]byte{"k": []byte("v")})

	// Single file: the only write fails.
	if err := Run(filepath.Join(d, "a.mpk"), false, &failingWriter{}); err == nil {
		t.Error("expected write error for single file")
	}
	// Search mode writes: opening brace, one entry, closing brace.
	for ok := 0; ok < 3; ok++ {
		if err := Run(d, true, &failingWriter{okWrites: ok}); err == nil {
			t.Errorf("expected write error after %d successful writes", ok)
		}
	}
	if err := Run(d, true, &failingWriter{okWrites: 3}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
