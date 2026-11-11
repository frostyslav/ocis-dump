// Package dump extracts files from OCIS storage into a POSIX directory tree.
package dump

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frostyslav/ocis-dump/internal/ocis"
)

// DefaultStoragePrefix is the spaces path within the OCIS data directory.
const DefaultStoragePrefix = "storage/users/spaces"

// Options are the parsed CLI options.
type Options struct {
	TopDir        string
	OutDir        string
	List          bool
	DryRun        bool
	User          string
	Username      string
	Info          bool
	Force         bool
	Jobs          int
	Verbose       bool
	Quiet         bool
	StoragePrefix string
}

// Stats accumulates copy statistics.
type Stats struct {
	Copied      int
	Skipped     int
	Errors      int
	Folders     int
	BytesCopied int64
	Elapsed     time.Duration
}

type logger struct {
	out     io.Writer
	err     io.Writer
	verbose bool
	quiet   bool
}

func (l *logger) info(format string, a ...any) {
	if !l.quiet {
		fmt.Fprintf(l.out, format+"\n", a...)
	}
}

func (l *logger) debug(format string, a ...any) {
	if l.verbose {
		fmt.Fprintf(l.out, format+"\n", a...)
	}
}

func (l *logger) warn(format string, a ...any) {
	if !l.quiet {
		fmt.Fprintf(l.err, "WARNING: "+format+"\n", a...)
	}
}

func (l *logger) error(format string, a ...any) {
	fmt.Fprintf(l.err, "ERROR: "+format+"\n", a...)
}

// spaceInfo holds metadata read from a space's root node.
type spaceInfo struct {
	Name, Type, Size, SizeUnit, User string
}

func readSpaceInfo(m ocis.MPK) spaceInfo {
	alias := m.Get("user.ocis.space.alias", "N/A")
	user := alias
	if parts := strings.Split(alias, "/"); len(parts) > 1 {
		user = parts[1]
	}
	size, _ := strconv.ParseInt(m.Get("user.ocis.treesize", "0"), 10, 64)
	hs, unit := ocis.FormatSize(size)
	return spaceInfo{
		Name:     m.Get("user.ocis.space.name", "N/A"),
		Type:     m.Get("user.ocis.space.type", "N/A"),
		Size:     hs,
		SizeUnit: unit,
		User:     user,
	}
}

type nodeInfo struct {
	ParentID *string
	BlobID   string
	Name     string
}

func readNodeInfo(m ocis.MPK) nodeInfo {
	n := nodeInfo{
		BlobID: m.Get("user.ocis.blobid", "N/A"),
		Name:   m.Get("user.ocis.name", "N/A"),
	}
	if v, ok := m["user.ocis.parentid"]; ok {
		s := string(v)
		n.ParentID = &s
	}
	return n
}

// resolver walks parent chains with caching.
type resolver struct {
	spaceID  string
	nodesDir string
	mpkCache map[string]nodeInfo
	paths    map[string]string
	log      *logger
}

func (r *resolver) info(mpkPath string) (nodeInfo, error) {
	if n, ok := r.mpkCache[mpkPath]; ok {
		return n, nil
	}
	m, err := ocis.LoadMPK(mpkPath)
	if err != nil {
		return nodeInfo{}, err
	}
	n := readNodeInfo(m)
	r.mpkCache[mpkPath] = n
	return n, nil
}

func joinPath(prefix string, names []string) string {
	if len(names) == 0 {
		return prefix
	}
	return prefix + "/" + strings.Join(names, "/")
}

// resolve returns the relative path (from the space root) of parentID.
func (r *resolver) resolve(parentID string) string {
	if p, ok := r.paths[parentID]; ok {
		return p
	}
	var names, chain []string // bottom-up
	prefix := "."
	seen := map[string]bool{}
	cur := &parentID

	for cur != nil && *cur != "" && *cur != r.spaceID {
		id := *cur
		if p, ok := r.paths[id]; ok {
			prefix = p
			break
		}
		if seen[id] {
			r.log.warn("Circular parent reference at node %s", id)
			break
		}
		seen[id] = true
		chain = append(chain, id)

		mpkPath, err := ocis.FindMPK(filepath.Join(r.nodesDir, ocis.Fourslashes(id)))
		var n nodeInfo
		if err == nil {
			n, err = r.info(mpkPath)
		}
		if err != nil {
			r.log.warn("Unresolvable parent %s in node chain", id)
			break
		}
		names = append(names, n.Name)
		cur = n.ParentID
	}

	// Cache every node of the chain: chain[i] is reached via names[i:] reversed.
	for i := range chain {
		sub := make([]string, 0, len(names)-i)
		for j := len(names) - 1; j >= i; j-- {
			sub = append(sub, names[j])
		}
		if _, ok := r.paths[chain[i]]; !ok {
			r.paths[chain[i]] = joinPath(prefix, sub)
		}
	}
	if len(chain) == 0 {
		r.paths[parentID] = prefix
	}
	return r.paths[parentID]
}

type fileEntry struct {
	ParentPath string
	Name       string
	BlobID     string
}

func buildFileTree(mpks []string, spaceID, nodesDir string, log *logger) []fileEntry {
	r := &resolver{
		spaceID:  spaceID,
		nodesDir: nodesDir,
		mpkCache: map[string]nodeInfo{},
		paths:    map[string]string{},
		log:      log,
	}
	var files []fileEntry
	for _, p := range mpks {
		n, err := r.info(p)
		if err != nil {
			log.warn("Skipping %s: %v", p, err)
			continue
		}
		if n.BlobID == "N/A" || n.ParentID == nil {
			continue // directory, or orphan
		}
		parent := "."
		if *n.ParentID != spaceID {
			parent = r.resolve(*n.ParentID)
		}
		files = append(files, fileEntry{ParentPath: parent, Name: n.Name, BlobID: n.BlobID})
	}
	return files
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func needsCopy(src, dst string) bool {
	dstSt, err := os.Stat(dst)
	if err != nil {
		return true
	}
	srcSt, err := os.Stat(src)
	if err != nil {
		return true
	}
	return srcSt.Size() != dstSt.Size() || !srcSt.ModTime().Equal(dstSt.ModTime())
}

// copyFile copies src to dst preserving the modification time.
func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return 0, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm()|0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	return n, os.Chtimes(dst, time.Now(), st.ModTime())
}

type task struct{ src, dst string }

func collectTasks(files []fileEntry, nodeDir string, sp spaceInfo, opt Options, log *logger) []task {
	var tasks []task
	absOut, _ := filepath.Abs(opt.OutDir)
	for _, f := range files {
		blob := filepath.Join(nodeDir, "blobs", ocis.Fourslashes(f.BlobID))
		if !isFile(blob) {
			continue
		}
		if opt.Verbose || opt.List {
			fmt.Fprintf(log.out, "  %s/%s\n", f.ParentPath, f.Name)
		}
		if opt.List {
			continue
		}
		dst := filepath.Join(absOut, sp.Type, sp.User, f.ParentPath, f.Name)
		if rel, err := filepath.Rel(absOut, dst); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			log.warn("Skipping %q: resolves outside output directory", f.Name)
			continue
		}
		tasks = append(tasks, task{blob, dst})
	}
	return tasks
}

func countFolders(files []fileEntry, nodeDir string) int {
	n := 0
	for _, f := range files {
		if !isFile(filepath.Join(nodeDir, "blobs", ocis.Fourslashes(f.BlobID))) {
			n++
		}
	}
	return n
}

func executeCopies(tasks []task, opt Options, stats *Stats, log *logger) {
	if opt.List {
		return
	}
	if opt.DryRun {
		var would []string
		for _, t := range tasks {
			if opt.Force || needsCopy(t.src, t.dst) {
				would = append(would, t.dst)
			}
		}
		log.info("  Dry run: %d would copy, %d would skip", len(would), len(tasks)-len(would))
		for _, p := range would {
			log.info("    %s", p)
		}
		return
	}

	type result struct {
		dst    string
		status string // copied | skipped | error
		n      int64
		err    error
	}
	jobs := max(opt.Jobs, 1)
	in := make(chan task)
	res := make(chan result)
	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range in {
				if err := os.MkdirAll(filepath.Dir(t.dst), 0o755); err != nil {
					res <- result{dst: t.dst, status: "error", err: err}
					continue
				}
				if !opt.Force && !needsCopy(t.src, t.dst) {
					res <- result{dst: t.dst, status: "skipped"}
					continue
				}
				n, err := copyFile(t.src, t.dst)
				if err != nil {
					res <- result{dst: t.dst, status: "error", err: err}
					continue
				}
				res <- result{dst: t.dst, status: "copied", n: n}
			}
		}()
	}
	go func() {
		for _, t := range tasks {
			in <- t
		}
		close(in)
		wg.Wait()
		close(res)
	}()

	for r := range res {
		name := filepath.Base(r.dst)
		switch r.status {
		case "copied":
			stats.Copied++
			stats.BytesCopied += r.n
			log.debug("    Saved %s", name)
		case "skipped":
			stats.Skipped++
			log.debug("    Skipped %s (unchanged)", name)
		default:
			stats.Errors++
			log.error("    FAILED %s: %v", name, r.err)
		}
	}
}

func matchesFilter(opt Options, sp spaceInfo, log *logger) bool {
	if opt.User != "" && !strings.Contains(strings.ToLower(sp.Name), strings.ToLower(opt.User)) {
		log.debug("Skipping space %s (user filter)", sp.Name)
		return false
	}
	if opt.Username != "" && !strings.Contains(strings.ToLower(sp.User), strings.ToLower(opt.Username)) {
		log.debug("Skipping space %s (username filter)", sp.User)
		return false
	}
	return true
}

// Run executes the dump.
func Run(opt Options, stdout, stderr io.Writer) error {
	log := &logger{out: stdout, err: stderr, verbose: opt.Verbose, quiet: opt.Quiet}

	if st, err := os.Stat(filepath.Join(opt.TopDir, "storage")); err != nil || !st.IsDir() {
		return fmt.Errorf("'storage' folder not found in %s", opt.TopDir)
	}
	log.info("OCIS root: %s", opt.TopDir)

	start := time.Now()
	var stats Stats

	nodes, _ := filepath.Glob(filepath.Join(opt.TopDir, opt.StoragePrefix, "*", "*", "nodes"))
	for _, node := range nodes {
		nodeDir := filepath.Dir(node)
		spaceID := filepath.Base(filepath.Dir(nodeDir)) + filepath.Base(nodeDir)
		rootID := filepath.Join(node, ocis.Fourslashes(spaceID))

		rootMPK, err := ocis.FindMPK(rootID)
		if err != nil {
			log.debug("No mpk for %s", rootID)
			continue
		}
		rootData, err := ocis.LoadMPK(rootMPK)
		if err != nil {
			log.warn("Unpack failed for %s", rootID)
			continue
		}
		sp := readSpaceInfo(rootData)
		if !matchesFilter(opt, sp, log) {
			continue
		}

		log.info("\n[%s/%s]", sp.Type, sp.Name)
		log.info("  username:  %s", sp.User)
		log.info("  root:      %s", rootID)
		log.info("  tree size: %s %s", sp.Size, sp.SizeUnit)

		if opt.Info {
			continue
		}
		files := buildFileTree(ocis.FindAllMPKs(nodeDir), spaceID, node, log)
		tasks := collectTasks(files, nodeDir, sp, opt, log)
		executeCopies(tasks, opt, &stats, log)
		stats.Folders += countFolders(files, nodeDir)
	}

	stats.Elapsed = time.Since(start)
	// The summary is shown in quiet mode too ("only errors and summary").
	fmt.Fprintln(stdout, "\n--- Summary ---")
	fmt.Fprintf(stdout, "Copied:  %d\n", stats.Copied)
	fmt.Fprintf(stdout, "Skipped: %d (unchanged)\n", stats.Skipped)
	fmt.Fprintf(stdout, "Errors:  %d\n", stats.Errors)
	fmt.Fprintf(stdout, "Folders: %d\n", stats.Folders)
	if stats.BytesCopied > 0 {
		hs, unit := ocis.FormatSize(stats.BytesCopied)
		fmt.Fprintf(stdout, "Data:    %s %s\n", hs, unit)
	}
	fmt.Fprintf(stdout, "Time:    %.1fs\n", stats.Elapsed.Seconds())
	return nil
}
