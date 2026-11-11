//go:build unix

package dump

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer that is safe to read while another goroutine writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestProgressWhileCopyIsBlocked proves progress keeps printing while no file
// has finished: the source is a FIFO, so the copy blocks until the test
// releases it.
func TestProgressWhileCopyIsBlocked(t *testing.T) {
	old := progressEvery
	progressEvery = 20 * time.Millisecond
	t.Cleanup(func() { progressEvery = old })

	d := t.TempDir()
	fifo := filepath.Join(d, "blob")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	dst := filepath.Join(d, "out", "slow.bin")

	out := &syncBuffer{}
	log := &logger{out: out, err: out}
	var st Stats
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		executeCopies([]task{{fifo, dst}}, Options{Jobs: 1}, &st, log)
	}()

	// No file has finished, yet progress lines must appear.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "progress: 0/1 (0%)") {
		if time.Now().After(deadline) {
			t.Fatalf("no progress while copy was blocked:\n%s", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Release the copy and make sure it completes normally.
	w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("data"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("copy did not finish")
	}
	if st.Copied != 1 || st.BytesCopied != 4 {
		t.Errorf("%+v", st)
	}
}
