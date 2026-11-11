// Package dump extracts files from OCIS storage into a POSIX directory tree.
package dump

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frostyslav/ocis-dump/internal/ocis"
)

// DefaultStoragePrefix is the spaces path within the OCIS data directory.
const DefaultStoragePrefix = "storage/users/spaces"

// DefaultFileProgressMinSize is the default size (bytes) from which an
// in-flight copy gets its own progress line.
const DefaultFileProgressMinSize int64 = 1 << 20

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

	// FileProgress adds a "copying <file> (x / y)" line to each progress report
	// for in-flight files of at least FileProgressMinSize bytes.
	FileProgress        bool
	FileProgressMinSize int64
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
	now     func() time.Time // clock for timestamps; time.Now when nil
}

// printf writes a timestamped line: "2006-01-02 15:04:05 <message>".
func (l *logger) printf(w io.Writer, format string, a ...any) {
	now := time.Now
	if l.now != nil {
		now = l.now
	}
	fmt.Fprintf(w, "%s "+format+"\n", append([]any{now().Format(time.DateTime)}, a...)...)
}

func (l *logger) info(format string, a ...any) {
	if !l.quiet {
		l.printf(l.out, format, a...)
	}
}

// progressEvery is the minimum time between two progress lines.
var progressEvery = 2 * time.Second

// ticker rate-limits progress output. The first call to due is always true
// after the interval has passed since creation, so short runs stay quiet.
type ticker struct {
	last time.Time
}

func newTicker() *ticker { return &ticker{last: time.Now()} }

func (t *ticker) due() bool {
	if now := time.Now(); now.Sub(t.last) >= progressEvery {
		t.last = now
		return true
	}
	return false
}

func (l *logger) debug(format string, a ...any) {
	if l.verbose {
		l.printf(l.out, format, a...)
	}
}

func (l *logger) warn(format string, a ...any) {
	if !l.quiet {
		l.printf(l.err, "WARNING: "+format, a...)
	}
}

func (l *logger) error(format string, a ...any) {
	l.printf(l.err, "ERROR: "+format, a...)
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
	tk := newTicker()
	for i, p := range mpks {
		if tk.due() {
			log.info("    reading metadata: %d/%d", i, len(mpks))
		}
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
	tk := newTicker()
	for i, f := range files {
		if tk.due() {
			log.info("    checking blobs: %d/%d", i, len(files))
		}
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

	jobs := max(opt.Jobs, 1)
	in := make(chan task)
	res := make(chan copyResult)
	flight := newFlight()
	var wg sync.WaitGroup
	for id := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range in {
				flight.start(id, t)
				r := processTask(t, opt.Force)
				flight.finish(id)
				res <- r
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

	log.info("  processing %d files with %d workers...", len(tasks), jobs)
	// Progress is driven by its own timer, not by finished files, so it keeps
	// reporting while a few large files are still being copied.
	tick := time.NewTicker(max(progressEvery, time.Millisecond))
	defer tick.Stop()
	start, baseBytes, done := time.Now(), stats.BytesCopied, 0
	for {
		select {
		case <-tick.C:
			reportProgress(log, opt, done, len(tasks), stats, baseBytes, start, flight.snapshot())
		case r, ok := <-res:
			if !ok {
				return
			}
			done++
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
}

type copyResult struct {
	dst    string
	status string // copied | skipped | error
	n      int64
	err    error
}

// processTask copies one file unless it is unchanged (and force is off).
func processTask(t task, force bool) copyResult {
	if err := os.MkdirAll(filepath.Dir(t.dst), 0o755); err != nil {
		return copyResult{dst: t.dst, status: "error", err: err}
	}
	if !force && !needsCopy(t.src, t.dst) {
		return copyResult{dst: t.dst, status: "skipped"}
	}
	n, err := copyFile(t.src, t.dst)
	if err != nil {
		return copyResult{dst: t.dst, status: "error", err: err}
	}
	return copyResult{dst: t.dst, status: "copied", n: n}
}

// flight tracks the files workers are currently copying.
type flight struct {
	mu    sync.Mutex
	files map[int]flightFile
}

type flightFile struct {
	dst   string
	total int64
}

// flightProgress is a snapshot of one in-flight copy.
type flightProgress struct {
	name        string
	done, total int64
}

func newFlight() *flight { return &flight{files: map[int]flightFile{}} }

func (f *flight) start(id int, t task) {
	var total int64
	if st, err := os.Stat(t.src); err == nil {
		total = st.Size()
	}
	f.mu.Lock()
	f.files[id] = flightFile{dst: t.dst, total: total}
	f.mu.Unlock()
}

func (f *flight) finish(id int) {
	f.mu.Lock()
	delete(f.files, id)
	f.mu.Unlock()
}

// snapshot returns the in-flight copies, sorted by name, with the bytes
// written so far (read from the size of the partially written destination).
func (f *flight) snapshot() []flightProgress {
	f.mu.Lock()
	cur := make([]flightFile, 0, len(f.files))
	for _, ff := range f.files {
		cur = append(cur, ff)
	}
	f.mu.Unlock()

	out := make([]flightProgress, 0, len(cur))
	for _, ff := range cur {
		var written int64
		if st, err := os.Stat(ff.dst); err == nil {
			written = min(st.Size(), ff.total)
		}
		out = append(out, flightProgress{name: filepath.Base(ff.dst), done: written, total: ff.total})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func humanSize(n int64) string {
	v, u := ocis.FormatSize(n)
	return v + " " + u
}

func reportProgress(log *logger, opt Options, done, total int, stats *Stats, baseBytes int64, start time.Time, inflight []flightProgress) {
	bytes := stats.BytesCopied - baseBytes
	for _, f := range inflight {
		bytes += f.done
	}
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	rate := ""
	if el := time.Since(start).Seconds(); el > 0 && bytes > 0 {
		rate = " @ " + humanSize(int64(float64(bytes)/el)) + "/s"
	}
	log.info("    progress: %d/%d (%d%%), copied %d, skipped %d, errors %d | %s%s",
		done, total, pct, stats.Copied, stats.Skipped, stats.Errors, humanSize(bytes), rate)
	if !opt.FileProgress {
		return
	}
	for _, f := range inflight {
		if f.total >= opt.FileProgressMinSize {
			log.info("      copying %s (%s / %s)", f.name, humanSize(f.done), humanSize(f.total))
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
	log.info("Found %d space(s)", len(nodes))
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

		if !opt.Quiet {
			fmt.Fprintln(stdout) // blank separator line, no timestamp
		}
		log.info("[%s/%s]", sp.Type, sp.Name)
		log.info("  username:  %s", sp.User)
		log.info("  root:      %s", rootID)
		log.info("  tree size: %s %s", sp.Size, sp.SizeUnit)

		if opt.Info {
			continue
		}
		log.info("  scanning metadata...")
		tk := newTicker()
		mpks := ocis.WalkMPKs(nodeDir, func(n int) {
			if tk.due() {
				log.info("    ...%d metadata files so far", n)
			}
		})
		log.info("  found %d metadata files, building file tree...", len(mpks))
		files := buildFileTree(mpks, spaceID, node, log)
		log.info("  found %d file entries, checking blobs...", len(files))
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
