package dump

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/frostyslav/ocis-dump/internal/ocis"
)

func writeMPK(t *testing.T, nodes, id string, m map[string]string) {
	t.Helper()
	b, err := msgpack.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(nodes, ocis.Fourslashes(id)) + ".mpk"
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeBlob(t *testing.T, nodeDir, blobID, content string) {
	t.Helper()
	p := filepath.Join(nodeDir, "blobs", ocis.Fourslashes(blobID))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (top string) {
	top = t.TempDir()
	nodeDir := filepath.Join(top, "storage/users/spaces/ab/cdef1234")
	nodes := filepath.Join(nodeDir, "nodes")
	space := "abcdef1234"
	writeMPK(t, nodes, space, map[string]string{
		"user.ocis.space.name":  "John Doe",
		"user.ocis.space.alias": "personal/jdoe",
		"user.ocis.space.type":  "personal",
		"user.ocis.treesize":    "2048",
	})
	writeMPK(t, nodes, "d1000000aa", map[string]string{"user.ocis.name": "docs", "user.ocis.parentid": space})
	writeMPK(t, nodes, "d2000000bb", map[string]string{"user.ocis.name": "sub", "user.ocis.parentid": "d1000000aa"})
	// Same file name in two directories: must not collide.
	writeMPK(t, nodes, "f1000000aa", map[string]string{"user.ocis.name": "a.txt", "user.ocis.parentid": "d2000000bb", "user.ocis.blobid": "b1000000aa"})
	writeMPK(t, nodes, "f2000000bb", map[string]string{"user.ocis.name": "a.txt", "user.ocis.parentid": "d1000000aa", "user.ocis.blobid": "b2000000bb"})
	writeMPK(t, nodes, "f3000000cc", map[string]string{"user.ocis.name": "root.txt", "user.ocis.parentid": space, "user.ocis.blobid": "b3000000cc"})
	writeBlob(t, nodeDir, "b1000000aa", "one")
	writeBlob(t, nodeDir, "b2000000bb", "two")
	writeBlob(t, nodeDir, "b3000000cc", "three")
	return top
}

func TestDump(t *testing.T) {
	top := fixture(t)
	out := t.TempDir()
	opt := Options{TopDir: top, OutDir: out, Jobs: 2, StoragePrefix: DefaultStoragePrefix}

	var so, se bytes.Buffer
	if err := Run(opt, &so, &se); err != nil {
		t.Fatalf("%v: %s", err, se.String())
	}
	for path, want := range map[string]string{
		"personal/jdoe/docs/sub/a.txt": "one",
		"personal/jdoe/docs/a.txt":     "two",
		"personal/jdoe/root.txt":       "three",
	} {
		got, err := os.ReadFile(filepath.Join(out, path))
		if err != nil || string(got) != want {
			t.Errorf("%s: got %q err %v, want %q", path, got, err, want)
		}
	}
	if !strings.Contains(so.String(), "Copied:  3") {
		t.Errorf("unexpected summary:\n%s", so.String())
	}

	// Second run: everything unchanged.
	so.Reset()
	_ = Run(opt, &so, &se)
	if !strings.Contains(so.String(), "Skipped: 3") {
		t.Errorf("expected 3 skipped:\n%s", so.String())
	}
}

func TestDryRunAndFilter(t *testing.T) {
	top := fixture(t)
	out := t.TempDir()
	var so, se bytes.Buffer
	_ = Run(Options{TopDir: top, OutDir: out, DryRun: true, Jobs: 1, StoragePrefix: DefaultStoragePrefix}, &so, &se)
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("dry run wrote files: %v", entries)
	}
	so.Reset()
	_ = Run(Options{TopDir: top, OutDir: out, User: "nobody", Jobs: 1, StoragePrefix: DefaultStoragePrefix}, &so, &se)
	if strings.Contains(so.String(), "John Doe") {
		t.Errorf("filter did not exclude space:\n%s", so.String())
	}
}

// ---------------------------------------------------------------------------
// Ports of the unit tests from the Python tests/test_dump.py
// ---------------------------------------------------------------------------

const (
	longSpaceID = "aabbccdd11223344aabbccdd11223344"
	blobIDLong  = "eeeeeeee11111111aaaaaaaa22222222"
)

func testLogger() (*logger, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return &logger{out: &out, err: &errb, verbose: true}, &out, &errb
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// copyWithTimes mimics shutil.copy2: content and mtime are copied.
func copyWithTimes(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
}

// --- readSpaceInfo -----------------------------------------------------

func TestReadSpaceInfo(t *testing.T) {
	t.Run("all fields", func(t *testing.T) {
		sp := readSpaceInfo(ocis.MPK{
			"user.ocis.space.name":  []byte("John Doe"),
			"user.ocis.space.alias": []byte("personal/jdoe"),
			"user.ocis.space.type":  []byte("personal"),
			"user.ocis.treesize":    []byte("5242880"),
		})
		want := spaceInfo{Name: "John Doe", Type: "personal", Size: "5.0", SizeUnit: "MiB", User: "jdoe"}
		if sp != want {
			t.Errorf("got %+v, want %+v", sp, want)
		}
	})
	t.Run("alias without slash", func(t *testing.T) {
		sp := readSpaceInfo(ocis.MPK{"user.ocis.space.alias": []byte("noslash")})
		if sp.User != "noslash" {
			t.Errorf("user = %q", sp.User)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		sp := readSpaceInfo(ocis.MPK{})
		if sp.Name != "N/A" || sp.Type != "N/A" || sp.Size != "0" || sp.SizeUnit != "bytes" {
			t.Errorf("got %+v", sp)
		}
	})
}

// --- readNodeInfo ------------------------------------------------------

func TestReadNodeInfo(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		n := readNodeInfo(ocis.MPK{
			"user.ocis.parentid": []byte("abc123def456"),
			"user.ocis.blobid":   []byte("blob789xyz"),
			"user.ocis.name":     []byte("document.pdf"),
		})
		if n.ParentID == nil || *n.ParentID != "abc123def456" || n.BlobID != "blob789xyz" || n.Name != "document.pdf" {
			t.Errorf("got %+v", n)
		}
	})
	t.Run("missing parent", func(t *testing.T) {
		n := readNodeInfo(ocis.MPK{"user.ocis.blobid": []byte("blob123"), "user.ocis.name": []byte("file.txt")})
		if n.ParentID != nil || n.BlobID != "blob123" || n.Name != "file.txt" {
			t.Errorf("got %+v", n)
		}
	})
	t.Run("missing blobid", func(t *testing.T) {
		n := readNodeInfo(ocis.MPK{"user.ocis.parentid": []byte("p"), "user.ocis.name": []byte("folder")})
		if n.BlobID != "N/A" {
			t.Errorf("blob = %q", n.BlobID)
		}
	})
}

// --- needsCopy ---------------------------------------------------------

func TestNeedsCopy(t *testing.T) {
	t.Run("missing dst", func(t *testing.T) {
		d := t.TempDir()
		writeFile(t, filepath.Join(d, "src"), "data")
		if !needsCopy(filepath.Join(d, "src"), filepath.Join(d, "dst")) {
			t.Error("expected true")
		}
	})
	t.Run("identical", func(t *testing.T) {
		d := t.TempDir()
		src, dst := filepath.Join(d, "src"), filepath.Join(d, "dst")
		writeFile(t, src, "data")
		copyWithTimes(t, src, dst)
		if needsCopy(src, dst) {
			t.Error("expected false")
		}
	})
	t.Run("different size", func(t *testing.T) {
		d := t.TempDir()
		src, dst := filepath.Join(d, "src"), filepath.Join(d, "dst")
		writeFile(t, src, "longer data here")
		writeFile(t, dst, "short")
		if !needsCopy(src, dst) {
			t.Error("expected true")
		}
	})
	t.Run("different mtime", func(t *testing.T) {
		d := t.TempDir()
		src, dst := filepath.Join(d, "src"), filepath.Join(d, "dst")
		writeFile(t, src, "data")
		writeFile(t, dst, "data")
		old := time.Now().Add(-100 * time.Second)
		if err := os.Chtimes(dst, old, old); err != nil {
			t.Fatal(err)
		}
		if !needsCopy(src, dst) {
			t.Error("expected true")
		}
	})
}

// --- copyFile ----------------------------------------------------------

func TestCopyFile(t *testing.T) {
	t.Run("copies content and mtime", func(t *testing.T) {
		d := t.TempDir()
		src, dst := filepath.Join(d, "blob"), filepath.Join(d, "file.txt")
		writeFile(t, src, "file content")
		old := time.Now().Add(-time.Hour).Truncate(time.Second)
		_ = os.Chtimes(src, old, old)
		n, err := copyFile(src, dst)
		if err != nil || n != 12 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		got, _ := os.ReadFile(dst)
		st, _ := os.Stat(dst)
		if string(got) != "file content" || !st.ModTime().Equal(old) {
			t.Errorf("content %q mtime %v", got, st.ModTime())
		}
	})
	t.Run("bad source", func(t *testing.T) {
		d := t.TempDir()
		if _, err := copyFile(filepath.Join(d, "nonexistent"), filepath.Join(d, "f")); err == nil {
			t.Error("expected error")
		}
	})
}

// --- matchesFilter -----------------------------------------------------

func TestMatchesFilter(t *testing.T) {
	log, _, _ := testLogger()
	sp := spaceInfo{Name: "John Doe", User: "jdoe"}
	tests := []struct {
		name string
		opt  Options
		want bool
	}{
		{"no filter", Options{}, true},
		{"user matches (case-insensitive)", Options{User: "john"}, true},
		{"user rejects", Options{User: "alice"}, false},
		{"username matches", Options{Username: "JDOE"}, true},
		{"username rejects", Options{Username: "alice"}, false},
	}
	for _, tc := range tests {
		if got := matchesFilter(tc.opt, sp, log); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// --- buildFileTree -----------------------------------------------------

func TestBuildFileTree(t *testing.T) {
	log, _, errb := testLogger()
	mpkPath := func(nodes, id string) string { return filepath.Join(nodes, ocis.Fourslashes(id)) + ".mpk" }

	t.Run("single file at root", func(t *testing.T) {
		nodes := filepath.Join(t.TempDir(), "nodes")
		writeMPK(t, nodes, "1111111122222222333333334444444a", map[string]string{
			"user.ocis.parentid": longSpaceID, "user.ocis.blobid": blobIDLong, "user.ocis.name": "readme.txt",
		})
		got := buildFileTree([]string{mpkPath(nodes, "1111111122222222333333334444444a")}, longSpaceID, nodes, log)
		want := []fileEntry{{ParentPath: ".", Name: "readme.txt", BlobID: blobIDLong}}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("file in subdirectory", func(t *testing.T) {
		nodes := filepath.Join(t.TempDir(), "nodes")
		dirID := "dddddddd11111111222222223333333a"
		fileID := "ffffffff11111111222222223333333a"
		writeMPK(t, nodes, dirID, map[string]string{
			"user.ocis.parentid": longSpaceID, "user.ocis.blobid": "N/A", "user.ocis.name": "Documents",
		})
		writeMPK(t, nodes, fileID, map[string]string{
			"user.ocis.parentid": dirID, "user.ocis.blobid": "bbbbbbbb11111111222222223333333a", "user.ocis.name": "report.pdf",
		})
		got := buildFileTree([]string{mpkPath(nodes, fileID)}, longSpaceID, nodes, log)
		if len(got) != 1 || got[0].ParentPath != "./Documents" || got[0].BlobID != "bbbbbbbb11111111222222223333333a" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("skips directory nodes", func(t *testing.T) {
		nodes := filepath.Join(t.TempDir(), "nodes")
		id := "dddddddd11111111222222223333333a"
		writeMPK(t, nodes, id, map[string]string{
			"user.ocis.parentid": longSpaceID, "user.ocis.blobid": "N/A", "user.ocis.name": "SomeFolder",
		})
		if got := buildFileTree([]string{mpkPath(nodes, id)}, longSpaceID, nodes, log); len(got) != 0 {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("corrupt mpk is skipped", func(t *testing.T) {
		nodes := filepath.Join(t.TempDir(), "nodes")
		bad := mpkPath(nodes, "bad0bad0bad0bad0bad0bad0bad0bad0")
		writeFile(t, bad, "not valid msgpack")
		if got := buildFileTree([]string{bad}, longSpaceID, nodes, log); len(got) != 0 {
			t.Errorf("got %+v", got)
		}
		if !strings.Contains(errb.String(), "Skipping") {
			t.Errorf("no warning logged: %q", errb.String())
		}
	})

	t.Run("same name in different dirs is kept", func(t *testing.T) {
		nodes := filepath.Join(t.TempDir(), "nodes")
		a := "aaaa111122223333444455556666777a"
		b := "bbbb111122223333444455556666777a"
		writeMPK(t, nodes, a, map[string]string{"user.ocis.parentid": longSpaceID, "user.ocis.blobid": "N/A", "user.ocis.name": "A"})
		writeMPK(t, nodes, "f1", map[string]string{"user.ocis.parentid": a, "user.ocis.blobid": "b1", "user.ocis.name": "same.txt"})
		writeMPK(t, nodes, b, map[string]string{"user.ocis.parentid": longSpaceID, "user.ocis.blobid": "b2", "user.ocis.name": "same.txt"})
		got := buildFileTree([]string{mpkPath(nodes, "f1"), mpkPath(nodes, b)}, longSpaceID, nodes, log)
		if len(got) != 2 {
			t.Errorf("got %+v", got)
		}
	})
}

// --- resolver ----------------------------------------------------------

func newResolver(nodes string) (*resolver, *bytes.Buffer) {
	log, _, errb := testLogger()
	return &resolver{
		spaceID:  longSpaceID,
		nodesDir: nodes,
		mpkCache: map[string]nodeInfo{},
		paths:    map[string]string{},
		log:      log,
	}, errb
}

func dirNode(t *testing.T, nodes, id, parent, name string) {
	t.Helper()
	writeMPK(t, nodes, id, map[string]string{"user.ocis.parentid": parent, "user.ocis.blobid": "N/A", "user.ocis.name": name})
}

func TestResolveParentPath(t *testing.T) {
	t.Run("circular reference", func(t *testing.T) {
		nodes := filepath.Join(t.TempDir(), "nodes")
		a, b := "aaaa111122223333444455556666777a", "bbbb111122223333444455556666777a"
		dirNode(t, nodes, a, b, "folderA")
		dirNode(t, nodes, b, a, "folderB")
		r, errb := newResolver(nodes)
		got := r.resolve(a)
		if !strings.Contains(got, "folderA") && !strings.Contains(got, "folderB") {
			t.Errorf("got %q", got)
		}
		if !strings.Contains(errb.String(), "Circular") {
			t.Errorf("no circular warning: %q", errb.String())
		}
	})
	t.Run("missing parent mpk", func(t *testing.T) {
		r, errb := newResolver(filepath.Join(t.TempDir(), "nodes"))
		if got := r.resolve("dead111122223333444455556666777a"); got != "." {
			t.Errorf("got %q", got)
		}
		if !strings.Contains(errb.String(), "Unresolvable") {
			t.Errorf("no warning: %q", errb.String())
		}
	})
	t.Run("parent is space root", func(t *testing.T) {
		r, _ := newResolver(filepath.Join(t.TempDir(), "nodes"))
		if got := r.resolve(longSpaceID); got != "." {
			t.Errorf("got %q", got)
		}
	})
	t.Run("cached path reused", func(t *testing.T) {
		nodes := filepath.Join(t.TempDir(), "nodes")
		id := "dddddddd11111111222222223333333a"
		dirNode(t, nodes, id, longSpaceID, "Documents")
		r, _ := newResolver(nodes)
		r1 := r.resolve(id)
		// Remove the mpk: a second call must be served from the cache.
		_ = os.Remove(filepath.Join(nodes, ocis.Fourslashes(id)) + ".mpk")
		if r2 := r.resolve(id); r1 != r2 || r1 != "./Documents" {
			t.Errorf("r1=%q r2=%q", r1, r2)
		}
	})
	t.Run("intermediate cache hit", func(t *testing.T) {
		nodes := filepath.Join(t.TempDir(), "nodes")
		gp, p, c := "aaaa111122223333444455556666777a", "bbbb111122223333444455556666777a", "cccc111122223333444455556666777a"
		dirNode(t, nodes, gp, longSpaceID, "Level1")
		dirNode(t, nodes, p, gp, "Level2")
		dirNode(t, nodes, c, p, "Level3")
		r, _ := newResolver(nodes)
		if got := r.resolve(gp); got != "./Level1" {
			t.Errorf("gp = %q", got)
		}
		if got := r.resolve(c); got != "./Level1/Level2/Level3" {
			t.Errorf("c = %q", got)
		}
		// Parent must now be cached: remove its mpk to prove it.
		_ = os.Remove(filepath.Join(nodes, ocis.Fourslashes(p)) + ".mpk")
		if got := r.resolve(p); got != "./Level1/Level2" {
			t.Errorf("p = %q", got)
		}
	})
	t.Run("deep chain without cache", func(t *testing.T) {
		nodes := filepath.Join(t.TempDir(), "nodes")
		gp, p, c := "aaaa111122223333444455556666777a", "bbbb111122223333444455556666777a", "cccc111122223333444455556666777a"
		dirNode(t, nodes, gp, longSpaceID, "L1")
		dirNode(t, nodes, p, gp, "L2")
		dirNode(t, nodes, c, p, "L3")
		r, _ := newResolver(nodes)
		if got := r.resolve(c); got != "./L1/L2/L3" {
			t.Errorf("got %q", got)
		}
		if r.paths[p] != "./L1/L2" || r.paths[gp] != "./L1" {
			t.Errorf("intermediates not cached: %v", r.paths)
		}
	})
}

// --- collectTasks / countFolders --------------------------------------

func TestCollectTasks(t *testing.T) {
	log, _, errb := testLogger()
	sp := spaceInfo{Type: "personal", User: "jdoe"}

	t.Run("existing blob", func(t *testing.T) {
		d := t.TempDir()
		blob := "aaaa111122223333444455556666777a"
		writeBlob(t, d, blob, "content")
		out := filepath.Join(d, "out")
		tasks := collectTasks([]fileEntry{{".", "test.txt", blob}}, d, sp, Options{OutDir: out}, log)
		if len(tasks) != 1 || filepath.Base(tasks[0].dst) != "test.txt" {
			t.Fatalf("tasks = %+v", tasks)
		}
		wantDst, _ := filepath.Abs(filepath.Join(out, "personal", "jdoe", "test.txt"))
		if tasks[0].dst != wantDst {
			t.Errorf("dst = %q, want %q", tasks[0].dst, wantDst)
		}
	})
	t.Run("missing blob skipped", func(t *testing.T) {
		d := t.TempDir()
		tasks := collectTasks([]fileEntry{{".", "ghost.txt", "nonexistent_blob_id_1234567a"}}, d, sp, Options{OutDir: d}, log)
		if len(tasks) != 0 {
			t.Errorf("tasks = %+v", tasks)
		}
	})
	t.Run("list mode creates no tasks", func(t *testing.T) {
		d := t.TempDir()
		writeBlob(t, d, "aaaa111122223333444455556666777a", "x")
		tasks := collectTasks([]fileEntry{{"./dir", "a.txt", "aaaa111122223333444455556666777a"}}, d, sp, Options{OutDir: d, List: true}, log)
		if len(tasks) != 0 {
			t.Errorf("tasks = %+v", tasks)
		}
	})
	t.Run("path traversal is rejected", func(t *testing.T) {
		d := t.TempDir()
		blob := "aaaa111122223333444455556666777a"
		writeBlob(t, d, blob, "x")
		tasks := collectTasks([]fileEntry{{".", "../../../evil", blob}}, d, sp, Options{OutDir: filepath.Join(d, "out")}, log)
		if len(tasks) != 0 || !strings.Contains(errb.String(), "outside output directory") {
			t.Errorf("tasks=%+v log=%q", tasks, errb.String())
		}
	})
}

func TestCountFolders(t *testing.T) {
	d := t.TempDir()
	files := []fileEntry{{".", "dir1", "fake_blob_1234567890123456789a"}, {".", "dir2", "fake_blob_abcdef1234567890abcda"}}
	if got := countFolders(files, d); got != 2 {
		t.Errorf("got %d", got)
	}
	writeBlob(t, d, "fake_blob_1234567890123456789a", "x")
	if got := countFolders(files, d); got != 1 {
		t.Errorf("got %d", got)
	}
}

// --- executeCopies -----------------------------------------------------

func TestExecuteCopies(t *testing.T) {
	setup := func(t *testing.T) (src, dst string) {
		d := t.TempDir()
		src, dst = filepath.Join(d, "blob"), filepath.Join(d, "out", "file.txt")
		writeFile(t, src, "data")
		return
	}

	t.Run("dry run does not copy", func(t *testing.T) {
		src, dst := setup(t)
		log, out, _ := testLogger()
		var st Stats
		executeCopies([]task{{src, dst}}, Options{DryRun: true}, &st, log)
		if _, err := os.Stat(dst); err == nil || st.Copied != 0 {
			t.Error("dry run wrote data")
		}
		if !strings.Contains(out.String(), "1 would copy") || !strings.Contains(out.String(), dst) {
			t.Errorf("output: %q", out.String())
		}
	})
	t.Run("dry run with force lists unchanged files", func(t *testing.T) {
		src, dst := setup(t)
		copyWithTimes(t, src, dst)
		log, out, _ := testLogger()
		var st Stats
		executeCopies([]task{{src, dst}}, Options{DryRun: true, Force: true}, &st, log)
		if !strings.Contains(out.String(), "1 would copy") {
			t.Errorf("output: %q", out.String())
		}
	})
	t.Run("dry run skips unchanged", func(t *testing.T) {
		src, dst := setup(t)
		copyWithTimes(t, src, dst)
		log, out, _ := testLogger()
		var st Stats
		executeCopies([]task{{src, dst}}, Options{DryRun: true}, &st, log)
		if !strings.Contains(out.String(), "0 would copy, 1 would skip") {
			t.Errorf("output: %q", out.String())
		}
	})
	t.Run("list mode does not copy", func(t *testing.T) {
		src, dst := setup(t)
		log, _, _ := testLogger()
		var st Stats
		executeCopies([]task{{src, dst}}, Options{List: true}, &st, log)
		if _, err := os.Stat(dst); err == nil {
			t.Error("list wrote data")
		}
	})
	t.Run("actual copy", func(t *testing.T) {
		src, dst := setup(t)
		log, out, _ := testLogger()
		var st Stats
		executeCopies([]task{{src, dst}}, Options{Jobs: 1, Verbose: true}, &st, log)
		got, _ := os.ReadFile(dst)
		if string(got) != "data" || st.Copied != 1 || st.BytesCopied != 4 {
			t.Errorf("content %q stats %+v", got, st)
		}
		if !strings.Contains(out.String(), "Saved file.txt") {
			t.Errorf("verbose output: %q", out.String())
		}
	})
	t.Run("skips unchanged", func(t *testing.T) {
		src, dst := setup(t)
		copyWithTimes(t, src, dst)
		log, out, _ := testLogger()
		var st Stats
		executeCopies([]task{{src, dst}}, Options{Jobs: 1, Verbose: true}, &st, log)
		if st.Skipped != 1 || st.Copied != 0 {
			t.Errorf("%+v", st)
		}
		if !strings.Contains(out.String(), "Skipped file.txt (unchanged)") {
			t.Errorf("verbose output: %q", out.String())
		}
	})
	t.Run("force overwrites", func(t *testing.T) {
		src, dst := setup(t)
		copyWithTimes(t, src, dst)
		log, _, _ := testLogger()
		var st Stats
		executeCopies([]task{{src, dst}}, Options{Jobs: 1, Force: true}, &st, log)
		if st.Copied != 1 {
			t.Errorf("%+v", st)
		}
	})
	t.Run("error on bad source", func(t *testing.T) {
		d := t.TempDir()
		log, _, errb := testLogger()
		var st Stats
		executeCopies([]task{{filepath.Join(d, "nonexistent"), filepath.Join(d, "out", "file.txt")}}, Options{Jobs: 1}, &st, log)
		if st.Errors != 1 || !strings.Contains(errb.String(), "FAILED") {
			t.Errorf("%+v log=%q", st, errb.String())
		}
	})
	t.Run("zero jobs falls back to one worker", func(t *testing.T) {
		src, dst := setup(t)
		log, _, _ := testLogger()
		var st Stats
		executeCopies([]task{{src, dst}}, Options{Jobs: 0}, &st, log)
		if st.Copied != 1 {
			t.Errorf("%+v", st)
		}
	})
	t.Run("many files in parallel", func(t *testing.T) {
		d := t.TempDir()
		var tasks []task
		for i := 0; i < 50; i++ {
			name := string(rune('a'+i%26)) + string(rune('a'+i/26))
			src := filepath.Join(d, "src", name)
			writeFile(t, src, name)
			tasks = append(tasks, task{src, filepath.Join(d, "out", name)})
		}
		log, _, _ := testLogger()
		var st Stats
		executeCopies(tasks, Options{Jobs: 8}, &st, log)
		if st.Copied != 50 || st.Errors != 0 {
			t.Errorf("%+v", st)
		}
	})
}

// --- Run (end-to-end) --------------------------------------------------

// longSpace builds the 32-char-ID layout used by the Python tests. If
// withFile is true a hello.txt node and blob are added.
func longSpace(t *testing.T, top string, withFile bool) {
	t.Helper()
	spaceDir := filepath.Join(top, "storage/users/spaces", longSpaceID[:16], longSpaceID[16:])
	nodes := filepath.Join(spaceDir, "nodes")
	writeMPK(t, nodes, longSpaceID, map[string]string{
		"user.ocis.space.name":  "TestUser",
		"user.ocis.space.alias": "personal/testuser",
		"user.ocis.space.type":  "personal",
		"user.ocis.treesize":    "1024",
	})
	if withFile {
		writeMPK(t, nodes, "1111111122222222333333334444444a", map[string]string{
			"user.ocis.parentid": longSpaceID, "user.ocis.blobid": blobIDLong, "user.ocis.name": "hello.txt",
		})
		writeBlob(t, spaceDir, blobIDLong, "hello world")
	}
}

func runDump(opt Options) (string, string, error) {
	if opt.StoragePrefix == "" {
		opt.StoragePrefix = DefaultStoragePrefix
	}
	if opt.Jobs == 0 {
		opt.Jobs = 2
	}
	var so, se bytes.Buffer
	err := Run(opt, &so, &se)
	return so.String(), se.String(), err
}

func TestRunInvalidTopDir(t *testing.T) {
	d := t.TempDir()
	_, _, err := runDump(Options{TopDir: d, OutDir: filepath.Join(d, "out")})
	if err == nil || !strings.Contains(err.Error(), "'storage' folder not found") {
		t.Errorf("err=%v", err)
	}
}

func TestRunInfoMode(t *testing.T) {
	top := t.TempDir()
	longSpace(t, top, true)
	out := filepath.Join(top, "out")
	so, _, err := runDump(Options{TopDir: top, OutDir: out, Info: true})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	for _, want := range []string{"[personal/TestUser]", "username:  testuser", "tree size: 1.0 KiB"} {
		if !strings.Contains(so, want) {
			t.Errorf("output missing %q:\n%s", want, so)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("info mode created output")
	}
}

func TestRunFullQuiet(t *testing.T) {
	top := t.TempDir()
	longSpace(t, top, true)
	out := filepath.Join(top, "output")
	so, _, err := runDump(Options{TopDir: top, OutDir: out, Quiet: true})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "personal", "testuser", "hello.txt"))
	if err != nil || string(got) != "hello world" {
		t.Errorf("content %q err %v", got, err)
	}
	if strings.Contains(so, "[personal/") || !strings.Contains(so, "--- Summary ---") || !strings.Contains(so, "Data:    11 bytes") {
		t.Errorf("quiet output:\n%s", so)
	}
}

func TestRunVerbose(t *testing.T) {
	top := t.TempDir()
	longSpace(t, top, true)
	so, _, _ := runDump(Options{TopDir: top, OutDir: filepath.Join(top, "output"), Verbose: true})
	for _, want := range []string{"./hello.txt", "Saved hello.txt"} {
		if !strings.Contains(so, want) {
			t.Errorf("output missing %q:\n%s", want, so)
		}
	}
}

func TestRunListMode(t *testing.T) {
	top := t.TempDir()
	longSpace(t, top, true)
	out := filepath.Join(top, "output")
	so, _, _ := runDump(Options{TopDir: top, OutDir: out, List: true})
	if !strings.Contains(so, "./hello.txt") {
		t.Errorf("list output:\n%s", so)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("list mode created output")
	}
}

func TestRunDryRunWithSpace(t *testing.T) {
	top := t.TempDir()
	longSpace(t, top, true)
	out := filepath.Join(top, "output")
	so, _, _ := runDump(Options{TopDir: top, OutDir: out, DryRun: true})
	if !strings.Contains(so, "1 would copy") {
		t.Errorf("output:\n%s", so)
	}
	if _, err := os.Stat(filepath.Join(out, "personal", "testuser", "hello.txt")); err == nil {
		t.Error("dry run wrote file")
	}
}

func TestRunUserFilterSkipsSpace(t *testing.T) {
	top := t.TempDir()
	longSpace(t, top, true)
	out := filepath.Join(top, "output")
	_, _, _ = runDump(Options{TopDir: top, OutDir: out, User: "nobody", Quiet: true})
	if _, err := os.Stat(out); err == nil {
		t.Error("filtered space was dumped")
	}
	_, _, _ = runDump(Options{TopDir: top, OutDir: out, Username: "nobody", Quiet: true})
	if _, err := os.Stat(out); err == nil {
		t.Error("username-filtered space was dumped")
	}
}

func TestRunCorruptRootMPKSkipped(t *testing.T) {
	top := t.TempDir()
	nodes := filepath.Join(top, "storage/users/spaces", longSpaceID[:16], longSpaceID[16:], "nodes")
	writeFile(t, filepath.Join(nodes, ocis.Fourslashes(longSpaceID))+".mpk", "not valid msgpack")
	out := filepath.Join(top, "output")
	_, se, err := runDump(Options{TopDir: top, OutDir: out})
	if err != nil || !strings.Contains(se, "Unpack failed") {
		t.Errorf("err=%v stderr=%q", err, se)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("corrupt space produced output")
	}
}

func TestRunMissingRootMPKSkipped(t *testing.T) {
	top := t.TempDir()
	nodes := filepath.Join(top, "storage/users/spaces", longSpaceID[:16], longSpaceID[16:], "nodes")
	if err := os.MkdirAll(filepath.Join(nodes, ocis.Fourslashes(longSpaceID)), 0o755); err != nil {
		t.Fatal(err)
	}
	so, _, err := runDump(Options{TopDir: top, OutDir: filepath.Join(top, "output"), Verbose: true})
	if err != nil || !strings.Contains(so, "No mpk for") {
		t.Errorf("err=%v output=%q", err, so)
	}
}

func TestRunCustomStoragePrefix(t *testing.T) {
	top := t.TempDir()
	if err := os.MkdirAll(filepath.Join(top, "storage"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Space lives under a custom prefix rather than the default one.
	spaceDir := filepath.Join(top, "custom/path", longSpaceID[:16], longSpaceID[16:])
	writeMPK(t, filepath.Join(spaceDir, "nodes"), longSpaceID, map[string]string{
		"user.ocis.space.name": "Custom", "user.ocis.space.alias": "personal/custom", "user.ocis.space.type": "personal",
	})
	so, _, _ := runDump(Options{TopDir: top, OutDir: filepath.Join(top, "out"), Info: true, StoragePrefix: "custom/path"})
	if !strings.Contains(so, "[personal/Custom]") {
		t.Errorf("output:\n%s", so)
	}
}

func TestRunFindsOnlyNodesAtExpectedDepth(t *testing.T) {
	top := t.TempDir()
	if err := os.MkdirAll(filepath.Join(top, "storage/users/spaces/part1/nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	so, _, _ := runDump(Options{TopDir: top, OutDir: filepath.Join(top, "out"), Verbose: true})
	if strings.Contains(so, "No mpk for") {
		t.Errorf("wrong-depth nodes dir was considered:\n%s", so)
	}
}

// --- summary -----------------------------------------------------------

func TestSummary(t *testing.T) {
	t.Run("without bytes omits data line", func(t *testing.T) {
		top := t.TempDir()
		if err := os.MkdirAll(filepath.Join(top, "storage"), 0o755); err != nil {
			t.Fatal(err)
		}
		so, _, _ := runDump(Options{TopDir: top, OutDir: top})
		if strings.Contains(so, "Data:") || !strings.Contains(so, "Copied:  0") || !strings.Contains(so, "Time:") {
			t.Errorf("summary:\n%s", so)
		}
	})
	t.Run("with bytes includes data line", func(t *testing.T) {
		top := t.TempDir()
		longSpace(t, top, false)
		spaceDir := filepath.Join(top, "storage/users/spaces", longSpaceID[:16], longSpaceID[16:])
		writeMPK(t, filepath.Join(spaceDir, "nodes"), "1111111122222222333333334444444a", map[string]string{
			"user.ocis.parentid": longSpaceID, "user.ocis.blobid": blobIDLong, "user.ocis.name": "big.bin",
		})
		writeBlob(t, spaceDir, blobIDLong, strings.Repeat("x", 5*1024*1024))
		so, _, _ := runDump(Options{TopDir: top, OutDir: filepath.Join(top, "out")})
		if !strings.Contains(so, "Data:    5.0 MiB") {
			t.Errorf("summary:\n%s", so)
		}
	})
	t.Run("second run counts skipped", func(t *testing.T) {
		top := t.TempDir()
		longSpace(t, top, true)
		opt := Options{TopDir: top, OutDir: filepath.Join(top, "out"), Quiet: true}
		_, _, _ = runDump(opt)
		so, _, _ := runDump(opt)
		if !strings.Contains(so, "Skipped: 1 (unchanged)") || !strings.Contains(so, "Copied:  0") {
			t.Errorf("summary:\n%s", so)
		}
	})
}

// --- extra error-path coverage ----------------------------------------

func TestNeedsCopyMissingSource(t *testing.T) {
	d := t.TempDir()
	dst := filepath.Join(d, "dst")
	writeFile(t, dst, "data")
	if !needsCopy(filepath.Join(d, "missing-src"), dst) {
		t.Error("expected true when the source cannot be stat'ed")
	}
}

func TestCopyFileErrors(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src")
	writeFile(t, src, "data")

	t.Run("destination directory missing", func(t *testing.T) {
		if _, err := copyFile(src, filepath.Join(d, "no", "such", "dir", "f")); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("source is a directory", func(t *testing.T) {
		if _, err := copyFile(d, filepath.Join(d, "out")); err == nil {
			t.Error("expected read error")
		}
	})
}

func TestExecuteCopiesMkdirError(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "blob")
	writeFile(t, src, "data")
	// A regular file where the destination directory should be.
	blocker := filepath.Join(d, "blocker")
	writeFile(t, blocker, "x")

	log, _, errb := testLogger()
	var st Stats
	executeCopies([]task{{src, filepath.Join(blocker, "sub", "file.txt")}}, Options{Jobs: 1}, &st, log)
	if st.Errors != 1 || !strings.Contains(errb.String(), "FAILED") {
		t.Errorf("%+v log=%q", st, errb.String())
	}
}

// --- progress output ---------------------------------------------------

func TestProgressOutput(t *testing.T) {
	old := progressEvery
	progressEvery = 0 // report on every opportunity
	t.Cleanup(func() { progressEvery = old })

	top := t.TempDir()
	longSpace(t, top, true)
	so, _, err := runDump(Options{TopDir: top, OutDir: filepath.Join(top, "out")})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Found 1 space(s)",
		"scanning metadata...",
		"metadata files so far",
		"found 2 metadata files, building file tree...",
		"reading metadata:",
		"found 1 file entries, checking blobs...",
		"checking blobs:",
		"processing 1 files with 2 workers...",
	} {
		if !strings.Contains(so, want) {
			t.Errorf("output missing %q:\n%s", want, so)
		}
	}
}

func TestProgressQuietAndFast(t *testing.T) {
	top := t.TempDir()
	longSpace(t, top, true)

	// Quiet: no phase or progress lines at all.
	so, _, _ := runDump(Options{TopDir: top, OutDir: filepath.Join(top, "q"), Quiet: true})
	if strings.Contains(so, "scanning metadata") || strings.Contains(so, "progress:") {
		t.Errorf("quiet output has progress:\n%s", so)
	}
	// Default interval: phase headers show, throttled progress lines do not.
	so, _, _ = runDump(Options{TopDir: top, OutDir: filepath.Join(top, "n")})
	if !strings.Contains(so, "scanning metadata...") || strings.Contains(so, "progress:") || strings.Contains(so, "so far") {
		t.Errorf("unexpected output:\n%s", so)
	}
	// Info mode stops before scanning.
	so, _, _ = runDump(Options{TopDir: top, OutDir: filepath.Join(top, "i"), Info: true})
	if strings.Contains(so, "scanning metadata") {
		t.Errorf("info mode scanned:\n%s", so)
	}
}

func TestTickerThrottles(t *testing.T) {
	old := progressEvery
	t.Cleanup(func() { progressEvery = old })

	progressEvery = time.Hour
	tk := newTicker()
	if tk.due() {
		t.Error("due immediately with a long interval")
	}
	progressEvery = 0
	if !tk.due() {
		t.Error("not due with a zero interval")
	}
}

// --- timestamps --------------------------------------------------------

func TestLoggerTimestamps(t *testing.T) {
	fixed := time.Date(2026, 11, 11, 14, 30, 5, 0, time.UTC)
	var out, errb bytes.Buffer
	log := &logger{out: &out, err: &errb, verbose: true, now: func() time.Time { return fixed }}

	log.info("hello %d", 1)
	log.debug("dbg")
	log.warn("careful")
	log.error("boom")

	if want := "2026-11-11 14:30:05 hello 1\n2026-11-11 14:30:05 dbg\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if want := "2026-11-11 14:30:05 WARNING: careful\n2026-11-11 14:30:05 ERROR: boom\n"; errb.String() != want {
		t.Errorf("stderr = %q, want %q", errb.String(), want)
	}
}

func TestLoggerDefaultClock(t *testing.T) {
	var out bytes.Buffer
	log := &logger{out: &out}
	log.info("x")
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} x\n$`).MatchString(out.String()) {
		t.Errorf("got %q", out.String())
	}
}

func TestRunLinesAreTimestampedExceptData(t *testing.T) {
	top := t.TempDir()
	longSpace(t, top, true)
	ts := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} `)

	so, _, err := runDump(Options{TopDir: top, OutDir: filepath.Join(top, "out")})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(so, "\n"), "\n") {
		// Blank separators and the summary block are plain; everything else is stamped.
		plain := line == "" || line == "--- Summary ---" || strings.HasPrefix(line, "Copied:") ||
			strings.HasPrefix(line, "Skipped:") || strings.HasPrefix(line, "Errors:") ||
			strings.HasPrefix(line, "Folders:") || strings.HasPrefix(line, "Data:") || strings.HasPrefix(line, "Time:")
		if !plain && !ts.MatchString(line) {
			t.Errorf("line without timestamp: %q", line)
		}
	}

	// --list output is data and must stay machine-readable.
	so, _, _ = runDump(Options{TopDir: top, OutDir: filepath.Join(top, "out2"), List: true})
	if !strings.Contains(so, "\n  ./hello.txt\n") {
		t.Errorf("list line was altered:\n%s", so)
	}

	// Quiet mode prints only the summary, with no stray blank separator.
	so, _, _ = runDump(Options{TopDir: top, OutDir: filepath.Join(top, "out3"), Quiet: true})
	if !strings.HasPrefix(so, "\n--- Summary ---\n") || strings.Contains(so, "\n\n") {
		t.Errorf("unexpected blank lines in quiet output:\n%q", so)
	}
}

// --- progress helpers --------------------------------------------------

func TestReportProgress(t *testing.T) {
	log, out, _ := testLogger()
	st := &Stats{Copied: 7, Skipped: 2, Errors: 1, BytesCopied: 10 * 1024 * 1024}
	inflight := []flightProgress{
		{name: "big.iso", done: 512 * 1024 * 1024, total: 1024 * 1024 * 1024},
		{name: "tiny.txt", done: 5, total: 10}, // below bigFile: not listed
	}
	opt := Options{FileProgress: true, FileProgressMinSize: DefaultFileProgressMinSize}
	reportProgress(log, opt, 10, 40, st, 0, time.Now().Add(-2*time.Second), inflight)

	s := out.String()
	for _, want := range []string{
		"progress: 10/40 (25%), copied 7, skipped 2, errors 1 | 522.0 MiB @ ",
		"/s",
		"copying big.iso (512.0 MiB / 1.0 GiB)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "tiny.txt") {
		t.Errorf("small file was listed:\n%s", s)
	}
}

func TestReportProgressNothingDoneYet(t *testing.T) {
	log, out, _ := testLogger()
	reportProgress(log, Options{}, 0, 0, &Stats{}, 0, time.Now(), nil)
	if !strings.Contains(out.String(), "progress: 0/0 (0%), copied 0, skipped 0, errors 0 | 0 bytes\n") {
		t.Errorf("got %q", out.String())
	}
}

func TestFlightSnapshot(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src.bin")
	writeFile(t, src, strings.Repeat("x", 100))
	dst := filepath.Join(d, "out", "b.bin")
	writeFile(t, dst, strings.Repeat("y", 40)) // partially written

	f := newFlight()
	f.start(1, task{src, dst})
	f.start(2, task{src, filepath.Join(d, "out", "a.bin")}) // not created yet

	snap := f.snapshot()
	if len(snap) != 2 || snap[0].name != "a.bin" || snap[0].done != 0 || snap[0].total != 100 ||
		snap[1].name != "b.bin" || snap[1].done != 40 {
		t.Errorf("snapshot = %+v", snap)
	}
	f.finish(1)
	f.finish(2)
	if len(f.snapshot()) != 0 {
		t.Error("finished files still in flight")
	}
}

func TestProcessTask(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src")
	writeFile(t, src, "data")
	dst := filepath.Join(d, "out", "f")

	if r := processTask(task{src, dst}, false); r.status != "copied" || r.n != 4 {
		t.Errorf("first: %+v", r)
	}
	if r := processTask(task{src, dst}, false); r.status != "skipped" {
		t.Errorf("second: %+v", r)
	}
	if r := processTask(task{src, dst}, true); r.status != "copied" {
		t.Errorf("forced: %+v", r)
	}
	if r := processTask(task{filepath.Join(d, "missing"), dst}, true); r.status != "error" || r.err == nil {
		t.Errorf("missing source: %+v", r)
	}
}

func TestReportProgressFileLineOptions(t *testing.T) {
	inflight := []flightProgress{
		{name: "small.bin", done: 1024, total: 2 * 1024},
		{name: "large.bin", done: 10 * 1024 * 1024, total: 100 * 1024 * 1024},
	}
	report := func(opt Options) string {
		log, out, _ := testLogger()
		reportProgress(log, opt, 1, 2, &Stats{}, 0, time.Now(), inflight)
		return out.String()
	}

	t.Run("disabled", func(t *testing.T) {
		s := report(Options{FileProgress: false, FileProgressMinSize: 0})
		if !strings.Contains(s, "progress: 1/2") || strings.Contains(s, "copying ") {
			t.Errorf("output:\n%s", s)
		}
	})
	t.Run("min size 0 lists everything", func(t *testing.T) {
		s := report(Options{FileProgress: true})
		if !strings.Contains(s, "copying small.bin") || !strings.Contains(s, "copying large.bin (10.0 MiB / 100.0 MiB)") {
			t.Errorf("output:\n%s", s)
		}
	})
	t.Run("min size filters", func(t *testing.T) {
		s := report(Options{FileProgress: true, FileProgressMinSize: 50 * 1024 * 1024})
		if !strings.Contains(s, "copying large.bin") || strings.Contains(s, "small.bin") {
			t.Errorf("output:\n%s", s)
		}
		s = report(Options{FileProgress: true, FileProgressMinSize: 200 * 1024 * 1024})
		if strings.Contains(s, "copying ") {
			t.Errorf("output:\n%s", s)
		}
	})
}
