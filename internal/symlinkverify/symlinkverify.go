// Package symlinkverify verifies (and optionally repairs) OCIS node symlinks.
package symlinkverify

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/frostyslav/ocis-dump/internal/ocis"
)

// Default sub-directories relative to the OCIS data root.
const (
	DefaultMetadataSubdir = "storage/metadata/spaces/"
	DefaultDataSubdir     = "storage/users/spaces/"
)

// Options are the parsed CLI options.
type Options struct {
	Path        string
	Fix         bool
	Verbose     bool
	Metadata    bool
	Data        bool
	MetadataDir string
	DataDir     string
}

// Stats accumulates verification results.
type Stats struct {
	Exist, Actual, Theoretical, Fixed int
}

type mpkInfo struct {
	Name, ParentID, Type, TypeName string
}

func (m mpkInfo) hasNA() bool {
	return m.Name == "N/A" || m.ParentID == "N/A" || m.Type == "N/A" || m.TypeName == "N/A"
}

func getInfo(m ocis.MPK) mpkInfo {
	i := mpkInfo{
		Name:     m.Get("user.ocis.name", "N/A"),
		ParentID: m.Get("user.ocis.parentid", "N/A"),
		Type:     m.Get("user.ocis.type", "N/A"),
	}
	switch i.Type {
	case "1":
		i.TypeName = "file"
	case "2":
		i.TypeName = "dir"
	default:
		i.TypeName = "N/A"
	}
	return i
}

type logger struct {
	w       io.Writer
	verbose bool
}

func (l logger) debug(f string, a ...any) {
	if l.verbose {
		fmt.Fprintf(l.w, f+"\n", a...)
	}
}
func (l logger) warn(f string, a ...any) { fmt.Fprintf(l.w, "WARNING: "+f+"\n", a...) }

// mpkFileToDir returns the directory for an mpk file (name without ".mpk").
func mpkFileToDir(p string) string {
	return filepath.Join(filepath.Dir(p), strings.TrimSuffix(filepath.Base(p), ".mpk"))
}

func findNodesAncestor(p string) (string, bool) {
	for cur := p; cur != filepath.Dir(cur); cur = filepath.Dir(cur) {
		if filepath.Base(cur) == "nodes" {
			return cur, true
		}
	}
	return "", false
}

// computeSymlinkPath returns where the symlink for the node should live. The
// ".." components are intentionally kept unresolved so the OS resolves them.
func computeSymlinkPath(i mpkInfo, asDir string) (string, error) {
	parent := ocis.Fourslashes(i.ParentID)

	var ref string
	switch i.TypeName {
	case "dir":
		ref = asDir
		if st, err := os.Stat(asDir); err == nil && st.Mode().IsRegular() {
			ref = filepath.Dir(asDir)
		}
	case "file":
		ref = filepath.Dir(asDir)
	default:
		return "", fmt.Errorf("%s has unexpected type: %s", asDir, i.TypeName)
	}

	depth := 5
	if nodes, ok := findNodesAncestor(ref); ok {
		if rel, err := filepath.Rel(nodes, ref); err == nil {
			depth = 0
			if rel != "." {
				depth = len(strings.Split(rel, string(filepath.Separator)))
			}
		}
	} else if i.TypeName == "file" {
		depth = 4
	}
	ups := strings.TrimSuffix(strings.Repeat("../", depth), "/")
	return strings.Join([]string{ref, ups, parent, i.Name}, "/"), nil
}

// resolveLoose is like Python's Path.resolve(): symlinks are followed for the
// existing part of the path and the remainder is appended lexically.
func resolveLoose(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	rest := ""
	cur := abs
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func isSymlink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func clearExisting(link string, i mpkInfo, directory string, log logger) {
	if exists(link) && !isSymlink(link) {
		log.debug("Removing non-symlink %s", filepath.Base(link))
		if i.TypeName == "dir" {
			_ = os.RemoveAll(link)
		} else {
			_ = os.Remove(link)
		}
	} else if !exists(directory) {
		log.debug("Creating missing node %s", filepath.Base(directory))
		if f, err := os.OpenFile(directory, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
		}
	}
	if i.TypeName == "dir" {
		_ = os.MkdirAll(resolveLoose(filepath.Dir(link)), 0o755)
	}
}

func createSymlink(link, target string, log logger) bool {
	err := os.Symlink(target, resolveLoose(link))
	switch {
	case err == nil:
		return true
	case errors.Is(err, fs.ErrExist):
		if isSymlink(link) {
			log.debug("%s is already a symlink", filepath.Base(link))
		} else {
			log.debug("Skipping %s (exists but not symlink)", filepath.Base(link))
		}
		return false
	case errors.Is(err, fs.ErrNotExist):
		log.warn("Target %s does not exist, skipping", target)
		return false
	case errors.Is(err, syscall.ENOTDIR):
		log.debug("Creating parent dir for %s", filepath.Base(link))
		parent := filepath.Dir(link)
		_ = os.Remove(parent)
		if os.MkdirAll(parent, 0o755) != nil {
			return false
		}
		return os.Symlink(target, resolveLoose(link)) == nil
	}
	return false
}

func attemptFix(link, target string, i mpkInfo, directory string, log logger) bool {
	clearExisting(link, i, directory, log)
	if !createSymlink(link, target, log) {
		return false
	}
	got, err := os.Readlink(link)
	return err == nil && filepath.Clean(got) == filepath.Clean(target)
}

func processMPK(file string, st *Stats, opt Options, log logger) {
	m, err := ocis.LoadMPK(file)
	if err != nil {
		log.warn("Skipping %s: %v", file, err)
		return
	}
	info := getInfo(m)
	if info.hasNA() {
		return
	}
	st.Theoretical++
	directory := mpkFileToDir(file)
	link, err := computeSymlinkPath(info, directory)
	if err != nil {
		log.warn("%v", err)
		return
	}
	// Equivalent of os.path.relpath(directory, link)[3:]
	rel, err := filepath.Rel(filepath.Clean(link), directory)
	if err != nil {
		log.warn("Skipping %s: %v", file, err)
		return
	}
	target := strings.TrimPrefix(rel, "../")

	if exists(link) {
		st.Exist++
	}
	if isSymlink(link) {
		st.Actual++
	}
	if !opt.Fix {
		return
	}
	if attemptFix(link, target, info, directory, log) {
		st.Fixed++
		log.debug("  Fixed: %s", filepath.Base(link))
	} else {
		log.debug("  Could not fix: %s", filepath.Base(link))
	}
}

// ErrIncorrect is returned when symlinks are still incorrect after verification.
var ErrIncorrect = errors.New("some symlinks may still be incorrect")

// Run executes the verification.
func Run(opt Options, stdout, stderr io.Writer) error {
	var path string
	switch {
	case opt.Metadata:
		path = filepath.Join(opt.Path, opt.MetadataDir)
	case opt.Data:
		path = filepath.Join(opt.Path, opt.DataDir)
	default:
		return errors.New("specify whether to check --metadata or --data")
	}
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return fmt.Errorf("invalid OCIS path: %s", path)
	}
	log := logger{w: stderr, verbose: opt.Verbose}
	fmt.Fprintf(stdout, "Checking symlinks at %s\n", path)

	var st Stats
	nodes, _ := filepath.Glob(filepath.Join(path, "*", "*", "nodes"))
	for _, n := range nodes {
		for _, f := range ocis.FindAllMPKs(n) {
			processMPK(f, &st, opt, log)
		}
	}

	fmt.Fprintln(stdout, "\n--- Results ---")
	fmt.Fprintf(stdout, "Exist:       %d\n", st.Exist)
	fmt.Fprintf(stdout, "Actual:      %d\n", st.Actual)
	fmt.Fprintf(stdout, "Theoretical: %d\n", st.Theoretical)
	if opt.Fix {
		fmt.Fprintf(stdout, "Fixed:       %d\n", st.Fixed)
	}
	if st.Exist != st.Theoretical || st.Actual != st.Theoretical {
		return ErrIncorrect
	}
	return nil
}
