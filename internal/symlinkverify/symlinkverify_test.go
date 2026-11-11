package symlinkverify

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/frostyslav/ocis-dump/internal/ocis"
)

func writeMPK(t *testing.T, path string, v map[string]string) {
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

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrIncorrect):
		return 2
	}
	return 1
}

func quiet() logger { return logger{w: &bytes.Buffer{}, verbose: true} }

func fileInfo(name string) mpkInfo {
	return mpkInfo{Name: name, ParentID: "aabbccdd11223344rest", Type: "1", TypeName: "file"}
}

func TestGetInfo(t *testing.T) {
	mk := func(name, parent, typ string) ocis.MPK {
		return ocis.MPK{
			"user.ocis.name":     []byte(name),
			"user.ocis.parentid": []byte(parent),
			"user.ocis.type":     []byte(typ),
		}
	}
	tests := []struct {
		in       ocis.MPK
		typeName string
	}{
		{mk("doc.pdf", "parent123", "1"), "file"},
		{mk("folder", "parent456", "2"), "dir"},
		{mk("weird", "p", "99"), "N/A"},
		{ocis.MPK{}, "N/A"},
	}
	for _, tc := range tests {
		if got := getInfo(tc.in); got.TypeName != tc.typeName {
			t.Errorf("TypeName = %q, want %q", got.TypeName, tc.typeName)
		}
	}
	got := getInfo(mk("doc.pdf", "parent123", "1"))
	if got.Name != "doc.pdf" || got.ParentID != "parent123" || got.Type != "1" {
		t.Errorf("unexpected info %+v", got)
	}
	empty := getInfo(ocis.MPK{})
	if empty.Name != "N/A" || empty.ParentID != "N/A" || !empty.hasNA() {
		t.Errorf("unexpected empty info %+v", empty)
	}
}

func TestMpkFileToDir(t *testing.T) {
	d := t.TempDir()
	if got := mpkFileToDir(filepath.Join(d, "ab", "cd", "node123.mpk")); got != filepath.Join(d, "ab", "cd", "node123") {
		t.Errorf("got %q", got)
	}
}

func TestFindNodesAncestor(t *testing.T) {
	d := t.TempDir()
	if got, ok := findNodesAncestor(filepath.Join(d, "nodes", "ab", "cd")); !ok || filepath.Base(got) != "nodes" {
		t.Errorf("got %q, %v", got, ok)
	}
	if _, ok := findNodesAncestor(filepath.Join(d, "some", "other")); ok {
		t.Error("expected no ancestor")
	}
}

func TestComputeSymlinkPath(t *testing.T) {
	d := t.TempDir()
	base := filepath.Join(d, "nodes", "ab", "cd", "ef", "12")

	t.Run("dir existing", func(t *testing.T) {
		dir := filepath.Join(base, "rest")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		info := mpkInfo{Name: "subfolder", ParentID: "aabbccdd11223344rest", TypeName: "dir"}
		got, err := computeSymlinkPath(info, dir)
		if err != nil {
			t.Fatal(err)
		}
		// 5 levels up from nodes/ab/cd/ef/12/rest -> nodes/..; then parent path.
		want := dir + "/../../../../../aa/bb/cc/dd/11223344rest/subfolder"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("dir not existing", func(t *testing.T) {
		info := mpkInfo{Name: "subfolder", ParentID: "aabbccdd11223344rest", TypeName: "dir"}
		got, err := computeSymlinkPath(info, filepath.Join(base, "ghost"))
		if err != nil || !strings.HasSuffix(got, "/subfolder") {
			t.Errorf("got %q, %v", got, err)
		}
	})

	t.Run("file", func(t *testing.T) {
		f := filepath.Join(base, "asfile")
		touch(t, f)
		got, err := computeSymlinkPath(fileInfo("test.txt"), f)
		if err != nil {
			t.Fatal(err)
		}
		// ref is the containing dir (nodes/ab/cd/ef/12): 4 levels up.
		want := base + "/../../../../aa/bb/cc/dd/11223344rest/test.txt"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("no nodes ancestor fallback", func(t *testing.T) {
		other := filepath.Join(d, "other", "ab", "cd", "ef", "12", "rest")
		if err := os.MkdirAll(other, 0o755); err != nil {
			t.Fatal(err)
		}
		got, _ := computeSymlinkPath(mpkInfo{Name: "x", ParentID: "aabbccdd11223344rest", TypeName: "dir"}, other)
		if !strings.Contains(got, "/../../../../../") {
			t.Errorf("dir fallback: %q", got)
		}
		got, _ = computeSymlinkPath(fileInfo("x"), other)
		if !strings.Contains(got, "/../../../../aa/") || strings.Contains(got, "/../../../../../") {
			t.Errorf("file fallback: %q", got)
		}
	})

	t.Run("unexpected type", func(t *testing.T) {
		_, err := computeSymlinkPath(mpkInfo{TypeName: "unknown"}, filepath.Join(d, "fake"))
		if err == nil || !strings.Contains(err.Error(), "unexpected type") {
			t.Errorf("got %v", err)
		}
	})
}

func TestRunPathSelection(t *testing.T) {
	var so, se bytes.Buffer
	d := t.TempDir()

	if code := exitCode(Run(Options{Path: d, MetadataDir: DefaultMetadataSubdir, DataDir: DefaultDataSubdir}, &so, &se)); code != 1 {
		t.Errorf("no mode: exit %d", code)
	}
	err := Run(Options{Path: d, Data: true, MetadataDir: DefaultMetadataSubdir, DataDir: DefaultDataSubdir}, &so, &se)
	if exitCode(err) != 1 || !strings.Contains(err.Error(), "invalid OCIS path") {
		t.Errorf("invalid path: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(d, "storage/metadata/spaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	so.Reset()
	if code := exitCode(Run(Options{Path: d, Metadata: true, MetadataDir: DefaultMetadataSubdir, DataDir: DefaultDataSubdir}, &so, &se)); code != 0 {
		t.Errorf("metadata: exit %d", code)
	}
	if !strings.Contains(so.String(), filepath.Join(d, "storage/metadata/spaces")) {
		t.Errorf("stdout: %s", so.String())
	}
}

func TestProcessMPKSkips(t *testing.T) {
	d := t.TempDir()

	bad := filepath.Join(d, "bad.mpk")
	if err := os.WriteFile(bad, []byte("not msgpack"), 0o644); err != nil {
		t.Fatal(err)
	}
	var st Stats
	processMPK(bad, &st, Options{}, quiet())
	if st.Theoretical != 0 {
		t.Error("invalid mpk counted")
	}

	partial := filepath.Join(d, "partial.mpk")
	writeMPK(t, partial, map[string]string{"user.ocis.name": "test"})
	processMPK(partial, &st, Options{}, quiet())
	if st.Theoretical != 0 {
		t.Error("incomplete mpk counted")
	}
}

func fileMPK(t *testing.T, root string) string {
	t.Helper()
	node := filepath.Join(root, "nodes", "ab", "cd", "ef", "12", "rest")
	if err := os.MkdirAll(node, 0o755); err != nil {
		t.Fatal(err)
	}
	// The parent node's directory must exist for a symlink to be placed in it.
	if err := os.MkdirAll(filepath.Join(root, "nodes/aa/bb/cc/dd/11223344rest"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := node + ".mpk"
	writeMPK(t, p, map[string]string{
		"user.ocis.name":     "file.txt",
		"user.ocis.parentid": "aabbccdd11223344rest",
		"user.ocis.type":     "1",
	})
	return p
}

func TestProcessMPKCountsTheoretical(t *testing.T) {
	var st Stats
	processMPK(fileMPK(t, t.TempDir()), &st, Options{}, quiet())
	if st.Theoretical != 1 || st.Exist != 0 || st.Actual != 0 {
		t.Errorf("%+v", st)
	}
}

func TestProcessMPKCountsExistingSymlink(t *testing.T) {
	p := fileMPK(t, t.TempDir())
	m, _ := ocis.LoadMPK(p)
	link, err := computeSymlinkPath(getInfo(m), mpkFileToDir(p))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	// Dangling symlink: counted as actual, not as existing.
	if err := os.Symlink("fake_target", link); err != nil {
		t.Fatal(err)
	}
	var st Stats
	processMPK(p, &st, Options{}, quiet())
	if st.Theoretical != 1 || st.Actual != 1 {
		t.Errorf("%+v", st)
	}
}

func TestProcessMPKFixCreatesSymlink(t *testing.T) {
	root := t.TempDir()
	p := fileMPK(t, root)
	var st Stats
	processMPK(p, &st, Options{Fix: true}, quiet())
	if st.Theoretical != 1 || st.Fixed != 1 {
		t.Fatalf("%+v", st)
	}
	link := filepath.Join(root, "nodes/aa/bb/cc/dd/11223344rest/file.txt")
	if target, err := os.Readlink(link); err != nil {
		t.Errorf("link not created: %v", err)
	} else if want := "../../../../../ab/cd/ef/12/rest"; target != want {
		t.Errorf("target %q, want %q", target, want)
	}
}

func TestClearExisting(t *testing.T) {
	file := mpkInfo{TypeName: "file"}
	dir := mpkInfo{TypeName: "dir"}

	t.Run("removes file", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target")
		touch(t, target)
		clearExisting(target, file, filepath.Join(d, "dir"), quiet())
		if _, err := os.Lstat(target); err == nil {
			t.Error("file still exists")
		}
	})
	t.Run("creates missing node", func(t *testing.T) {
		d := t.TempDir()
		node := filepath.Join(d, "missing_node")
		clearExisting(filepath.Join(d, "link"), file, node, quiet())
		if _, err := os.Stat(node); err != nil {
			t.Error("node not created")
		}
	})
	t.Run("removes directory", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target_dir")
		touch(t, filepath.Join(target, "child.txt"))
		clearExisting(target, dir, filepath.Join(d, "dir"), quiet())
		if _, err := os.Lstat(target); err == nil {
			t.Error("directory still exists")
		}
	})
	t.Run("creates parent for dir type", func(t *testing.T) {
		d := t.TempDir()
		node := filepath.Join(d, "node")
		touch(t, node)
		link := filepath.Join(d, "parent", "link")
		clearExisting(link, dir, node, quiet())
		if st, err := os.Stat(filepath.Dir(link)); err != nil || !st.IsDir() {
			t.Error("parent not created")
		}
	})
}

func TestCreateSymlink(t *testing.T) {
	t.Run("creates", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target")
		touch(t, target)
		link := filepath.Join(d, "link")
		if !createSymlink(link, target, quiet()) || !isSymlink(link) {
			t.Error("symlink not created")
		}
	})
	t.Run("existing symlink", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target")
		touch(t, target)
		link := filepath.Join(d, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if createSymlink(link, target, quiet()) {
			t.Error("expected false")
		}
	})
	t.Run("existing regular file", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target")
		link := filepath.Join(d, "link")
		touch(t, target)
		touch(t, link)
		if createSymlink(link, target, quiet()) {
			t.Error("expected false")
		}
	})
	t.Run("missing parent dir", func(t *testing.T) {
		d := t.TempDir()
		if createSymlink(filepath.Join(d, "no_parent", "link"), filepath.Join(d, "x", "target"), quiet()) {
			t.Error("expected false")
		}
	})
	t.Run("parent is a file", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target")
		touch(t, target)
		parent := filepath.Join(d, "parent_dir")
		touch(t, parent)
		link := filepath.Join(parent, "link")
		if !createSymlink(link, target, quiet()) || !isSymlink(link) {
			t.Error("retry with mkdir failed")
		}
	})
}

func TestAttemptFix(t *testing.T) {
	file := mpkInfo{TypeName: "file"}

	t.Run("success", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target")
		touch(t, target)
		link := filepath.Join(d, "link")
		if !attemptFix(link, target, file, target, quiet()) || !isSymlink(link) {
			t.Error("fix failed")
		}
	})
	t.Run("replaces regular file", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target")
		link := filepath.Join(d, "link")
		touch(t, target)
		touch(t, link)
		if !attemptFix(link, target, file, target, quiet()) {
			t.Error("fix failed")
		}
	})
	t.Run("replaces directory", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "target")
		link := filepath.Join(d, "link")
		touch(t, target)
		if err := os.Mkdir(link, 0o755); err != nil {
			t.Fatal(err)
		}
		if !attemptFix(link, target, mpkInfo{TypeName: "dir"}, target, quiet()) {
			t.Error("fix failed")
		}
	})
	t.Run("failure", func(t *testing.T) {
		d := t.TempDir()
		node := filepath.Join(d, "node")
		touch(t, node)
		link := filepath.Join(d, "also_nonexistent", "link")
		if attemptFix(link, filepath.Join(d, "nonexistent_deep", "target"), file, node, quiet()) {
			t.Error("expected failure")
		}
	})
}

func dataFixture(t *testing.T) (root, nodes string) {
	root = t.TempDir()
	nodes = filepath.Join(root, "storage/users/spaces/part1/part2/nodes")
	if err := os.MkdirAll(nodes, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, nodes
}

func opts(root string) Options {
	return Options{Path: root, Data: true, MetadataDir: DefaultMetadataSubdir, DataDir: DefaultDataSubdir}
}

func TestRunEmptyTreeSucceeds(t *testing.T) {
	root, _ := dataFixture(t)
	var so, se bytes.Buffer
	o := opts(root)
	o.Verbose = true
	if code := exitCode(Run(o, &so, &se)); code != 0 {
		t.Errorf("exit %d", code)
	}
}

func TestRunMismatchExits2(t *testing.T) {
	root, nodes := dataFixture(t)
	fileMPK(t, filepath.Dir(nodes)) // creates <part2>/nodes/...
	var so, se bytes.Buffer
	if code := exitCode(Run(opts(root), &so, &se)); code != 2 {
		t.Errorf("exit %d", code)
	}
	for _, want := range []string{"Exist:", "Actual:", "Theoretical: 1"} {
		if !strings.Contains(so.String(), want) {
			t.Errorf("stdout missing %q: %s", want, so.String())
		}
	}
	if strings.Contains(so.String(), "Fixed:") {
		t.Error("Fixed printed without --fix")
	}
}

func TestRunFixRepairsAndSucceeds(t *testing.T) {
	root, nodes := dataFixture(t)
	fileMPK(t, filepath.Dir(nodes))
	var so, se bytes.Buffer
	o := opts(root)
	o.Fix = true
	_ = Run(o, &so, &se) // first pass repairs
	if !strings.Contains(so.String(), "Fixed:       1") {
		t.Fatalf("stdout: %s", so.String())
	}
	so.Reset()
	if code := exitCode(Run(opts(root), &so, &se)); code != 0 {
		t.Errorf("verification after fix: exit %d\n%s", code, so.String())
	}
}

func TestComputeSymlinkPathDirThatIsAFile(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "nodes", "ab", "cd", "ef", "12", "rest")
	touch(t, f)
	info := mpkInfo{Name: "sub", ParentID: "aabbccdd11223344rest", TypeName: "dir"}
	got, err := computeSymlinkPath(info, f)
	if err != nil {
		t.Fatal(err)
	}
	// A dir node stored as a plain file is anchored at its parent directory.
	if !strings.HasPrefix(got, filepath.Dir(f)+"/") {
		t.Errorf("got %q", got)
	}
}

func TestCreateSymlinkOtherErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions are not enforced for root")
	}
	t.Run("permission denied", func(t *testing.T) {
		d := t.TempDir()
		ro := filepath.Join(d, "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
		if createSymlink(filepath.Join(ro, "link"), "target", quiet()) {
			t.Error("expected false")
		}
	})
	t.Run("grandparent is a file", func(t *testing.T) {
		d := t.TempDir()
		blocker := filepath.Join(d, "blocker")
		touch(t, blocker)
		if createSymlink(filepath.Join(blocker, "sub", "link"), "target", quiet()) {
			t.Error("expected false")
		}
	})
}

func TestProcessMPKFixCannotFix(t *testing.T) {
	root := t.TempDir()
	node := filepath.Join(root, "nodes", "ab", "cd", "ef", "12", "rest")
	if err := os.MkdirAll(node, 0o755); err != nil {
		t.Fatal(err)
	}
	p := node + ".mpk"
	// The parent node directory does not exist, so no symlink can be created.
	writeMPK(t, p, map[string]string{
		"user.ocis.name":     "file.txt",
		"user.ocis.parentid": "aabbccdd11223344rest",
		"user.ocis.type":     "1",
	})
	var st Stats
	processMPK(p, &st, Options{Fix: true}, quiet())
	if st.Theoretical != 1 || st.Fixed != 0 {
		t.Errorf("%+v", st)
	}
}
