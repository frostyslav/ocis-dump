package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/frostyslav/ocis-dump/internal/ocis"
)

func run(args ...string) (code int, stdout, stderr string) {
	var so, se bytes.Buffer
	code = Execute("test", args, &so, &se)
	return code, so.String(), se.String()
}

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

// ocisFixture creates a one-space OCIS tree with a single hello.txt file.
func ocisFixture(t *testing.T) string {
	t.Helper()
	top := t.TempDir()
	const space, blob = "abcdef1234", "b1000000aa"
	spaceDir := filepath.Join(top, "storage/users/spaces/ab/cdef1234")
	nodes := filepath.Join(spaceDir, "nodes")
	mpk := func(id string) string { return filepath.Join(nodes, ocis.Fourslashes(id)+".mpk") }

	writeMPK(t, mpk(space), map[string]string{
		"user.ocis.space.name": "John Doe", "user.ocis.space.alias": "personal/jdoe", "user.ocis.space.type": "personal",
	})
	writeMPK(t, mpk("f1000000aa"), map[string]string{
		"user.ocis.name": "hello.txt", "user.ocis.parentid": space, "user.ocis.blobid": blob, "user.ocis.type": "1",
	})
	bp := filepath.Join(spaceDir, "blobs", ocis.Fourslashes(blob))
	if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bp, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	return top
}

func TestVersionAndHelp(t *testing.T) {
	code, so, _ := run("--version")
	if code != 0 || !strings.Contains(so, "test") {
		t.Errorf("version: code=%d out=%q", code, so)
	}
	code, so, _ = run("--help")
	for _, sub := range []string{"dump", "mpk", "symlinks"} {
		if code != 0 || !strings.Contains(so, sub) {
			t.Errorf("help missing %q: %s", sub, so)
		}
	}
}

func TestUnknownCommandAndFlag(t *testing.T) {
	if code, _, se := run("bogus"); code != 1 || !strings.Contains(se, "Error:") {
		t.Errorf("unknown command: code=%d stderr=%q", code, se)
	}
	if code, _, _ := run("dump", "--no-such-flag"); code != 1 {
		t.Errorf("unknown flag: code=%d", code)
	}
}

func TestVerboseQuietMutuallyExclusive(t *testing.T) {
	code, _, se := run("dump", "-v", "-q", t.TempDir())
	if code != 1 || !strings.Contains(se, "none of the others can be") {
		t.Errorf("code=%d stderr=%q", code, se)
	}
}

func TestDumpListAndDryRunMutuallyExclusive(t *testing.T) {
	if code, _, _ := run("dump", "-l", "-n", t.TempDir()); code != 1 {
		t.Errorf("code=%d", code)
	}
}

func TestDumpArgValidation(t *testing.T) {
	if code, _, _ := run("dump", "a", "b", "c"); code != 1 {
		t.Errorf("too many args: code=%d", code)
	}
	if code, _, se := run("dump", "-j", "0", t.TempDir()); code != 1 || !strings.Contains(se, "--jobs") {
		t.Errorf("jobs: code=%d stderr=%q", code, se)
	}
	if code, _, se := run("dump", t.TempDir()); code != 1 || !strings.Contains(se, "'storage' folder not found") {
		t.Errorf("invalid topdir: code=%d stderr=%q", code, se)
	}
}

func TestDumpEndToEnd(t *testing.T) {
	top := ocisFixture(t)
	out := filepath.Join(t.TempDir(), "out")

	code, so, se := run("dump", top, out, "-j", "2")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, se)
	}
	got, err := os.ReadFile(filepath.Join(out, "personal", "jdoe", "hello.txt"))
	if err != nil || string(got) != "hello" {
		t.Errorf("content %q err %v", got, err)
	}
	if !strings.Contains(so, "Copied:  1") {
		t.Errorf("summary:\n%s", so)
	}
}

func TestDumpFlags(t *testing.T) {
	top := ocisFixture(t)
	out := filepath.Join(t.TempDir(), "out")

	t.Run("quiet hides space header, keeps summary", func(t *testing.T) {
		_, so, _ := run("dump", "-q", top, out)
		if strings.Contains(so, "[personal/") || !strings.Contains(so, "--- Summary ---") {
			t.Errorf("output:\n%s", so)
		}
	})
	t.Run("user filter", func(t *testing.T) {
		o := filepath.Join(t.TempDir(), "o")
		run("dump", "-u", "nobody", top, o)
		if _, err := os.Stat(o); err == nil {
			t.Error("filtered space dumped")
		}
	})
	t.Run("username filter", func(t *testing.T) {
		o := filepath.Join(t.TempDir(), "o")
		if code, _, _ := run("dump", "--username", "jdoe", top, o); code != 0 {
			t.Fatal("run failed")
		}
		if _, err := os.Stat(filepath.Join(o, "personal/jdoe/hello.txt")); err != nil {
			t.Error("matching space not dumped")
		}
	})
	t.Run("dry run", func(t *testing.T) {
		o := filepath.Join(t.TempDir(), "o")
		_, so, _ := run("dump", "-n", top, o)
		if !strings.Contains(so, "1 would copy") {
			t.Errorf("output:\n%s", so)
		}
		if _, err := os.Stat(o); err == nil {
			t.Error("dry run wrote output")
		}
	})
	t.Run("list", func(t *testing.T) {
		_, so, _ := run("dump", "-l", top, filepath.Join(t.TempDir(), "o"))
		if !strings.Contains(so, "./hello.txt") {
			t.Errorf("output:\n%s", so)
		}
	})
	t.Run("info", func(t *testing.T) {
		_, so, _ := run("dump", "-i", top)
		if !strings.Contains(so, "username:  jdoe") {
			t.Errorf("output:\n%s", so)
		}
	})
}

func TestMpk(t *testing.T) {
	top := ocisFixture(t)
	spaces := filepath.Join(top, "storage/users/spaces")
	file := ocis.Fourslashes("f1000000aa")
	mpkFile := filepath.Join(spaces, "ab/cdef1234/nodes", file+".mpk")

	t.Run("single file", func(t *testing.T) {
		code, so, _ := run("mpk", mpkFile)
		if code != 0 || !strings.Contains(so, `b"user.ocis.name": b"hello.txt"`) {
			t.Errorf("code=%d out=%q", code, so)
		}
	})
	t.Run("search directory to file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "out.txt")
		code, so, se := run("mpk", "-s", spaces, "-o", out)
		if code != 0 || so != "" {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, so, se)
		}
		got, _ := os.ReadFile(out)
		if !strings.Contains(string(got), "hello.txt") || !strings.Contains(string(got), "John Doe") {
			t.Errorf("file content:\n%s", got)
		}
	})
	t.Run("search on a file fails", func(t *testing.T) {
		if code, _, se := run("mpk", "-s", mpkFile); code != 1 || !strings.Contains(se, "asked to search directory") {
			t.Errorf("code=%d stderr=%q", code, se)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if code, _, se := run("mpk", filepath.Join(t.TempDir(), "nope.mpk")); code != 1 || !strings.Contains(se, "does not exist") {
			t.Errorf("code=%d stderr=%q", code, se)
		}
	})
	t.Run("requires an argument", func(t *testing.T) {
		if code, _, _ := run("mpk"); code != 1 {
			t.Errorf("code=%d", code)
		}
	})
	t.Run("bad output path", func(t *testing.T) {
		if code, _, _ := run("mpk", mpkFile, "-o", filepath.Join(t.TempDir(), "no/such/dir/out")); code != 1 {
			t.Errorf("code=%d", code)
		}
	})
}

func TestSymlinks(t *testing.T) {
	t.Run("requires --data or --metadata", func(t *testing.T) {
		if code, _, se := run("symlinks", t.TempDir()); code != 1 || !strings.Contains(se, "metadata") {
			t.Errorf("code=%d stderr=%q", code, se)
		}
	})
	t.Run("--data and --metadata are exclusive", func(t *testing.T) {
		if code, _, _ := run("symlinks", t.TempDir(), "-d", "-m"); code != 1 {
			t.Errorf("code=%d", code)
		}
	})
	t.Run("invalid path", func(t *testing.T) {
		if code, _, se := run("symlinks", t.TempDir(), "-d"); code != 1 || !strings.Contains(se, "invalid OCIS path") {
			t.Errorf("code=%d stderr=%q", code, se)
		}
	})
	t.Run("exit 2 when incorrect, 0 after fix", func(t *testing.T) {
		top := ocisFixture(t)
		// Add a directory node so a symlink is expected in the root dir.
		nodes := filepath.Join(top, "storage/users/spaces/ab/cdef1234/nodes")
		writeMPK(t, filepath.Join(nodes, ocis.Fourslashes("f1000000aa")+".mpk"), map[string]string{
			"user.ocis.name": "hello.txt", "user.ocis.parentid": "abcdef1234", "user.ocis.blobid": "b1000000aa", "user.ocis.type": "1",
		})
		if err := os.MkdirAll(filepath.Join(nodes, ocis.Fourslashes("abcdef1234")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(nodes, ocis.Fourslashes("f1000000aa")), 0o755); err != nil {
			t.Fatal(err)
		}

		code, so, se := run("symlinks", top, "-d")
		if code != 2 || !strings.Contains(se, "WARNING") || !strings.Contains(so, "Theoretical: 1") {
			t.Fatalf("verify: code=%d stdout=%q stderr=%q", code, so, se)
		}
		if code, so, _ := run("symlinks", top, "-d", "--fix"); !strings.Contains(so, "Fixed:       1") {
			t.Fatalf("fix: code=%d stdout=%q", code, so)
		}
		if code, so, se := run("symlinks", top, "-d"); code != 0 {
			t.Errorf("after fix: code=%d stdout=%q stderr=%q", code, so, se)
		}
	})
}

func TestDumpDefaultTopDir(t *testing.T) {
	t.Run("uses $HOME/.ocis", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		code, _, se := run("dump")
		want := filepath.Join(home, ".ocis")
		if code != 1 || !strings.Contains(se, "'storage' folder not found in "+want) {
			t.Errorf("code=%d stderr=%q", code, se)
		}
	})
	t.Run("works when $HOME/.ocis is a valid store", func(t *testing.T) {
		top := ocisFixture(t)
		home := t.TempDir()
		if err := os.Symlink(top, filepath.Join(home, ".ocis")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		code, so, se := run("dump", "-i")
		if code != 0 || !strings.Contains(so, "username:  jdoe") {
			t.Errorf("code=%d stdout=%q stderr=%q", code, so, se)
		}
	})
	t.Run("no home directory", func(t *testing.T) {
		t.Setenv("HOME", "")
		code, _, se := run("dump")
		if code != 1 || !strings.Contains(se, "cannot determine default topdir") {
			t.Errorf("code=%d stderr=%q", code, se)
		}
	})
}

func TestDumpFileProgressFlags(t *testing.T) {
	top := ocisFixture(t)
	out := func() string { return filepath.Join(t.TempDir(), "o") }

	t.Run("min size must be valid", func(t *testing.T) {
		code, _, se := run("dump", "--file-progress-min-size", "lots", top, out())
		if code != 1 || !strings.Contains(se, "--file-progress-min-size") {
			t.Errorf("code=%d stderr=%q", code, se)
		}
	})
	t.Run("accepts sizes and disabling", func(t *testing.T) {
		for _, args := range [][]string{
			{"dump", "--file-progress-min-size", "512KiB", top, out()},
			{"dump", "--file-progress-min-size=0", top, out()},
			{"dump", "--file-progress=false", top, out()},
		} {
			if code, _, se := run(args...); code != 0 {
				t.Errorf("%v: code=%d stderr=%q", args, code, se)
			}
		}
	})
	t.Run("env vars set the defaults", func(t *testing.T) {
		t.Setenv("OCIS_DUMP_FILE_PROGRESS", "false")
		t.Setenv("OCIS_DUMP_FILE_PROGRESS_MIN_SIZE", "10MiB")
		dumpCmd, _, err := NewRootCmd("test").Find([]string{"dump"})
		if err != nil {
			t.Fatal(err)
		}
		if got := dumpCmd.Flags().Lookup("file-progress").DefValue; got != "false" {
			t.Errorf("file-progress default = %q, want false", got)
		}
		if got := dumpCmd.Flags().Lookup("file-progress-min-size").DefValue; got != "10MiB" {
			t.Errorf("min-size default = %q, want 10MiB", got)
		}
		if code, _, se := run("dump", top, out()); code != 0 {
			t.Errorf("code=%d stderr=%q", code, se)
		}
	})
	t.Run("flags override env vars", func(t *testing.T) {
		t.Setenv("OCIS_DUMP_FILE_PROGRESS_MIN_SIZE", "garbage")
		if code, _, _ := run("dump", top, out()); code != 1 {
			t.Errorf("garbage env should fail, code=%d", code)
		}
		if code, _, se := run("dump", "--file-progress-min-size=1MiB", top, out()); code != 0 {
			t.Errorf("flag should win over env: code=%d stderr=%q", code, se)
		}
	})
	t.Run("invalid env bool", func(t *testing.T) {
		t.Setenv("OCIS_DUMP_FILE_PROGRESS", "maybe")
		code, _, se := run("dump", top, out())
		if code != 1 || !strings.Contains(se, "OCIS_DUMP_FILE_PROGRESS") {
			t.Errorf("code=%d stderr=%q", code, se)
		}
	})
}
