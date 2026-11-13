// Package tail follows a growing log file, like tail -F.
package tail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"
)

// maxLine is the longest line Follow emits. Longer ones are dropped: a
// client decides how long a request line is, the agent's memory should not.
const maxLine = 64 << 10

// Follow calls emit for every complete line appended to path until ctx is
// done. It starts at the end of the file, waits for the file if it does not
// exist, and keeps going across rotation (rename and recreate) and
// truncation.
//
// NOTE: polling, and no saved offset. Lines written while the agent is down
// are skipped. A checkpoint file fixes that when it starts to matter.
// Truncation is seen only while the file is shorter than the read offset:
// one truncated and refilled past that point within a single poll is read
// from the old offset. Keep poll short; rename-style rotation has no such gap.
func Follow(ctx context.Context, path string, poll time.Duration, emit func(line string)) error {
	t := tailer{emit: emit}
	defer t.close()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for atStart := true; ; atStart = false {
		if err := t.step(path, atStart); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

type tailer struct {
	emit     func(string)
	f        *os.File
	buf      []byte
	skipping bool // inside a line that already went over maxLine
}

func (t *tailer) close() {
	if t.f != nil {
		t.f.Close()
		t.f = nil
	}
	t.buf, t.skipping = nil, false
}

// step does one poll: open the file if needed, read what is new, and notice
// rotation or truncation.
func (t *tailer) step(path string, atStart bool) error {
	if t.f == nil {
		f, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		t.f = f
		// Only the file that was there at start is old news. One that
		// shows up later is read from its first line.
		if atStart {
			if _, err := f.Seek(0, io.SeekEnd); err != nil {
				return err
			}
		}
	}
	if err := t.drain(); err != nil {
		return err
	}

	onDisk, statErr := os.Stat(path)
	open, err := t.f.Stat()
	if err != nil {
		return err
	}
	if statErr != nil || !os.SameFile(onDisk, open) {
		// Rotated. Pick up what was written between the last read and
		// the rename, then let the next step open the new file.
		err := t.drain()
		t.close()
		return err
	}
	if pos, err := t.f.Seek(0, io.SeekCurrent); err != nil {
		return err
	} else if pos > onDisk.Size() {
		// Truncated in place (logrotate copytruncate).
		t.buf, t.skipping = nil, false
		_, err := t.f.Seek(0, io.SeekStart)
		return err
	}
	return nil
}

// drain reads to the end of the file and emits the complete lines.
func (t *tailer) drain() error {
	chunk := make([]byte, 32<<10)
	for {
		n, err := t.f.Read(chunk)
		t.buf = append(t.buf, chunk[:n]...)
		t.lines()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (t *tailer) lines() {
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			if len(t.buf) > maxLine {
				t.buf, t.skipping = t.buf[:0], true
			}
			return
		}
		if !t.skipping && i <= maxLine {
			t.emit(string(t.buf[:i]))
		}
		t.skipping = false
		t.buf = t.buf[i+1:]
	}
}
