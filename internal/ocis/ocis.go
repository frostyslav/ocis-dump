// Package ocis contains OCIS storage primitives shared by the tools: MPK
// metadata decoding, node-ID path layout and size formatting.
package ocis

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	kib = 1024
	mib = 1024 * 1024
	gib = 1024 * 1024 * 1024
)

// MPK is the decoded content of an OCIS .mpk metadata file.
type MPK map[string][]byte

// Get returns the value for key as a string, or def when the key is absent.
func (m MPK) Get(key, def string) string {
	if v, ok := m[key]; ok {
		return string(v)
	}
	return def
}

// Fourslashes splits an OCIS node ID into the 2/2/2/2/rest directory layout.
func Fourslashes(s string) string {
	if s == "" {
		return ""
	}
	parts := make([]string, 0, 5)
	for i := 0; i < 8; i += 2 {
		parts = append(parts, clip(s, i, i+2))
	}
	parts = append(parts, clip(s, 8, len(s)))
	return strings.Join(parts, "/")
}

// clip slices s[from:to] with Python-like leniency for out-of-range indexes.
func clip(s string, from, to int) string {
	if from > len(s) {
		from = len(s)
	}
	if to > len(s) {
		to = len(s)
	}
	return s[from:to]
}

// LoadMPK reads and decodes a MessagePack file.
func LoadMPK(path string) (MPK, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := msgpack.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unpack failed for file %s: %w", path, err)
	}
	// Non-string values (e.g. integers) are kept in their text form.
	m := make(MPK, len(raw))
	for k, v := range raw {
		switch t := v.(type) {
		case string:
			m[k] = []byte(t)
		case []byte:
			m[k] = t
		default:
			m[k] = []byte(fmt.Sprint(t))
		}
	}
	return m, nil
}

// LoadMPKRaw reads a MessagePack file into a generic map (used for viewing).
func LoadMPKRaw(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := msgpack.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unpack failed for file %s: %w", path, err)
	}
	return m, nil
}

// FindAllMPKs walks root and returns every *.mpk file path.
func FindAllMPKs(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".mpk") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// FindMPK locates the .mpk file for a node path. It tries "<path>.mpk" first,
// then falls back to any .mpk file in the parent directory.
func FindMPK(path string) (string, error) {
	trivial := path + ".mpk"
	if _, err := os.Stat(trivial); err == nil {
		return trivial, nil
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.mpk"))
	sort.Strings(matches)
	for _, m := range matches {
		if _, err := os.Stat(m); err == nil {
			return m, nil
		}
	}
	return "", fmt.Errorf("no mpk file found for node: %s: %w", path, fs.ErrNotExist)
}

// FormatSize converts a byte count to a human readable (value, unit) pair.
func FormatSize(n int64) (string, string) {
	switch {
	case n >= gib:
		return round2(float64(n) / gib), "GiB"
	case n >= mib:
		return round2(float64(n) / mib), "MiB"
	case n >= kib:
		return round2(float64(n) / kib), "KiB"
	}
	return strconv.FormatInt(n, 10), "bytes"
}

func round2(f float64) string {
	s := strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
