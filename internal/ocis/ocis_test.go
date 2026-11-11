package ocis

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestFourslashes(t *testing.T) {
	tests := map[string]string{
		"abcdef1234567890abcdef1234567890": "ab/cd/ef/12/34567890abcdef1234567890",
		"abcdefgh":                         "ab/cd/ef/gh/",
		"12345678":                         "12/34/56/78/",
		"":                                 "",
	}
	for in, want := range tests {
		if got := Fourslashes(in); got != want {
			t.Errorf("Fourslashes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFourslashesShortInputDoesNotPanic(t *testing.T) {
	if got := Fourslashes("abc"); got != "ab/c///" {
		t.Errorf("got %q", got)
	}
}

func writeMsgpack(t *testing.T, path string, v any) {
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

func TestLoadMPK(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.mpk")
	writeMsgpack(t, p, map[string]any{"key": "value", "number": 42})
	m, err := LoadMPK(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(m["key"]) != "value" || string(m["number"]) != "42" {
		t.Errorf("unexpected content: %v", m)
	}
	if m.Get("missing", "N/A") != "N/A" {
		t.Error("default not returned")
	}
}

func TestLoadMPKBinaryValues(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bin.mpk")
	writeMsgpack(t, p, map[string][]byte{"user.ocis.name": []byte("Test User")})
	m, err := LoadMPK(p)
	if err != nil || m.Get("user.ocis.name", "") != "Test User" {
		t.Errorf("got %v, %v", m, err)
	}
}

func TestLoadMPKInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.mpk")
	if err := os.WriteFile(p, []byte("this is not msgpack"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadMPK(p)
	if err == nil || !strings.Contains(err.Error(), "unpack failed") {
		t.Errorf("expected unpack failure, got %v", err)
	}
}

func TestLoadMPKMissingFile(t *testing.T) {
	_, err := LoadMPK(filepath.Join(t.TempDir(), "nope.mpk"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected not-exist error, got %v", err)
	}
}

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindAllMPKs(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"a.mpk", "sub/b.mpk", "sub/deep/c.mpk", "readme.txt", "data.json"} {
		touch(t, filepath.Join(d, f))
	}
	var names []string
	for _, p := range FindAllMPKs(d) {
		names = append(names, filepath.Base(p))
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "a.mpk,b.mpk,c.mpk" {
		t.Errorf("got %v", names)
	}
}

func TestFindAllMPKsEmpty(t *testing.T) {
	if got := FindAllMPKs(t.TempDir()); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestFindMPKTrivial(t *testing.T) {
	d := t.TempDir()
	node := filepath.Join(d, "ab", "cd", "ef", "12", "rest")
	if err := os.MkdirAll(node, 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, node+".mpk")
	got, err := FindMPK(node)
	if err != nil || got != node+".mpk" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindMPKFallbackToSibling(t *testing.T) {
	d := t.TempDir()
	sibling := filepath.Join(d, "nodes", "other.mpk")
	touch(t, sibling)
	got, err := FindMPK(filepath.Join(d, "nodes", "nonexistent"))
	if err != nil || got != sibling {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestFindMPKNotFound(t *testing.T) {
	d := t.TempDir()
	_, err := FindMPK(filepath.Join(d, "nowhere"))
	if err == nil || !strings.Contains(err.Error(), "no mpk file found") || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("got %v", err)
	}
}

func TestFormatSize(t *testing.T) {
	const kib, mib, gib = 1024, 1024 * 1024, 1024 * 1024 * 1024
	tests := []struct {
		in        int64
		val, unit string
	}{
		{0, "0", "bytes"},
		{512, "512", "bytes"},
		{2048, "2.0", "KiB"},
		{kib, "1.0", "KiB"},
		{5 * mib, "5.0", "MiB"},
		{mib, "1.0", "MiB"},
		{3 * gib, "3.0", "GiB"},
		{gib, "1.0", "GiB"},
		{1536, "1.5", "KiB"},
		{11078159600, "10.32", "GiB"},
	}
	for _, tc := range tests {
		v, u := FormatSize(tc.in)
		if v != tc.val || u != tc.unit {
			t.Errorf("FormatSize(%d) = (%s, %s), want (%s, %s)", tc.in, v, u, tc.val, tc.unit)
		}
	}
}

func TestLoadMPKRaw(t *testing.T) {
	d := t.TempDir()
	good := filepath.Join(d, "good.mpk")
	writeMsgpack(t, good, map[string]any{"name": "x", "n": 42})
	m, err := LoadMPKRaw(good)
	if err != nil || m["name"] != "x" || m["n"] == nil {
		t.Errorf("got %v, %v", m, err)
	}

	bad := filepath.Join(d, "bad.mpk")
	if err := os.WriteFile(bad, []byte("not msgpack"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMPKRaw(bad); err == nil || !strings.Contains(err.Error(), "unpack failed") {
		t.Errorf("invalid file: %v", err)
	}
	if _, err := LoadMPKRaw(filepath.Join(d, "nope.mpk")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
}

func TestFindAllMPKsSkipsUnreadableDirs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions are not enforced for root")
	}
	d := t.TempDir()
	touch(t, filepath.Join(d, "ok.mpk"))
	locked := filepath.Join(d, "locked")
	touch(t, filepath.Join(locked, "hidden.mpk"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	got := FindAllMPKs(d)
	if len(got) != 1 || filepath.Base(got[0]) != "ok.mpk" {
		t.Errorf("got %v", got)
	}
}

func TestWalkMPKsReportsProgress(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"a.mpk", "sub/b.mpk", "sub/c.mpk", "skip.txt"} {
		touch(t, filepath.Join(d, f))
	}
	var counts []int
	got := WalkMPKs(d, func(n int) { counts = append(counts, n) })
	if len(got) != 3 || len(counts) != 3 || counts[0] != 1 || counts[2] != 3 {
		t.Errorf("got %v, counts %v", got, counts)
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"4096", 4096},
		{"512b", 512},
		{"512 bytes", 512},
		{"1k", 1024},
		{"1KiB", 1024},
		{"1KB", 1000},
		{"1MiB", 1024 * 1024},
		{"1 MiB", 1024 * 1024},
		{"1.5MiB", 1572864},
		{"10mb", 10_000_000},
		{"2GiB", 2 * 1024 * 1024 * 1024},
		{"1GB", 1_000_000_000},
		{"  3 g ", 3 * 1024 * 1024 * 1024},
	}
	for _, tc := range tests {
		got, err := ParseSize(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}
}

func TestParseSizeErrors(t *testing.T) {
	for _, in := range []string{"", "abc", "MiB", "-1MiB", "1XB", "1.2.3MiB", "NaN", "1 M iB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q): expected error", in)
		}
	}
}

func TestParseSizeRoundTripsFormatSize(t *testing.T) {
	for _, n := range []int64{512, 2048, 5 * 1024 * 1024, 3 * 1024 * 1024 * 1024} {
		v, u := FormatSize(n)
		got, err := ParseSize(v + u)
		if err != nil || got != n {
			t.Errorf("round trip %d -> %s%s -> %d (%v)", n, v, u, got, err)
		}
	}
}
