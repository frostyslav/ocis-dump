// Package mpkview prints the decoded contents of OCIS .mpk files.
package mpkview

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/frostyslav/ocis-dump/internal/ocis"
)

// FormatMap renders a decoded mpk map with sorted keys, one entry per line.
func FormatMap(m map[string]any, indent string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("{\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s  b%s: %s,\n", indent, strconv.Quote(k), formatValue(m[k]))
	}
	sb.WriteString(indent + "}")
	return sb.String()
}

func formatValue(v any) string {
	switch t := v.(type) {
	case string:
		return "b" + strconv.Quote(t)
	case []byte:
		return "b" + strconv.Quote(string(t))
	default:
		return fmt.Sprintf("%v", t)
	}
}

// Run views a single file, or (search=true) every mpk under a directory.
func Run(path string, search bool, w io.Writer) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file does not exist: %s", path)
	}
	if search && !st.IsDir() {
		return errors.New("file provided, but asked to search directory")
	}
	if search {
		files := ocis.FindAllMPKs(path)
		all := make(map[string]any, len(files))
		for _, f := range files {
			m, err := ocis.LoadMPKRaw(f)
			if err != nil {
				return err
			}
			all[f] = m
		}
		return writeNested(w, all)
	}
	m, err := ocis.LoadMPKRaw(path)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, FormatMap(m, ""))
	return err
}

func writeNested(w io.Writer, all map[string]any) error {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if _, err := fmt.Fprintln(w, "{"); err != nil {
		return err
	}
	for _, k := range keys {
		m := all[k].(map[string]any)
		if _, err := fmt.Fprintf(w, "  %s: %s,\n", strconv.Quote(k), FormatMap(m, "  ")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "}")
	return err
}
