package tail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// follow starts Follow on path and returns a function that waits for the
// next line.
func follow(t *testing.T, path string) (next func() string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	lines := make(chan string, 100)
	done := make(chan error, 1)
	go func() {
		done <- Follow(ctx, path, time.Millisecond, func(l string) { lines <- l })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Follow: %v", err)
		}
	})
	// Let Follow open the file and seek to its end before the test writes.
	time.Sleep(100 * time.Millisecond)
	return func() string {
		t.Helper()
		select {
		case l := <-lines:
			return l
		case <-time.After(2 * time.Second):
			t.Fatal("no line within 2s")
			return ""
		}
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func expect(t *testing.T, next func() string, want ...string) {
	t.Helper()
	for _, w := range want {
		if got := next(); got != w {
			t.Fatalf("line = %q, want %q", got, w)
		}
	}
}

func TestFollowEmitsOnlyNewCompleteLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "old line\n")
	next := follow(t, path)

	appendTo(t, path, "one\ntw")
	expect(t, next, "one")
	appendTo(t, path, "o\nthree\n")
	expect(t, next, "two", "three")
}

func TestFollowSurvivesRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "old line\n")
	next := follow(t, path)

	appendTo(t, path, "last line of the old file\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, "first line of the new file\n")
	expect(t, next, "last line of the old file", "first line of the new file")
}

func TestFollowSurvivesTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "a long line that existed before the agent started\n")
	next := follow(t, path)

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	appendTo(t, path, "after\n")
	expect(t, next, "after")
}

func TestFollowWaitsForAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	next := follow(t, path)

	appendTo(t, path, "created later\n")
	expect(t, next, "created later")
}

// A client controls how long a log line gets. An endless one must not grow
// the buffer without bound.
func TestFollowDropsOversizedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	next := follow(t, path)

	appendTo(t, path, strings.Repeat("x", maxLine+1))
	time.Sleep(20 * time.Millisecond)
	appendTo(t, path, strings.Repeat("y", maxLine)+"\nshort\n")
	appendTo(t, path, strings.Repeat("z", maxLine+1)+"\nshort again\n")
	expect(t, next, "short", "short again")
}
