package daemon

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The session logs stay within three limits from the user's config,
// checked when a daemon starts and every hour while it runs. All three
// apply at once, and whichever a session meets first acts on it:
//
//   - log.days: sessions older than this are deleted. The default, 0, keeps
//     them: at about 285 bytes an action, 2 GB holds millions of actions,
//     so the size limits alone keep the folder small, and an age limit
//     would only throw history away. It is there for anyone who wants
//     history to expire.
//   - log.max: plain .jsonl files may take this much; past it, the oldest
//     are gzipped into .jsonl.gz, a tenth of the size or less.
//   - log.archive_max: gzipped files may take this much; past it, the
//     oldest are deleted.

// busy is how recently a file may have been written and still be gzipped:
// one written to within it is in use.
const busy = time.Hour

type logFile struct {
	path string
	size int64
	mod  time.Time
}

// logFiles lists the session logs, oldest first.
func logFiles(dir string) []logFile {
	var out []logFile
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !(strings.HasSuffix(n, ".jsonl") || strings.HasSuffix(n, ".jsonl.gz")) {
			continue
		}
		if fi, err := e.Info(); err == nil {
			out = append(out, logFile{path: filepath.Join(dir, n), size: fi.Size(), mod: fi.ModTime()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].mod.Before(out[j].mod) })
	return out
}

// readLog reads a session log, gzipped or not.
func readLog(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(path, ".gz") {
		return data, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// tidyLogs applies the limits now and then every hour until the daemon stops.
func (s *server) tidyLogs() {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		s.auditMu.Lock() // not while a line is being written
		l := LoadConfig().Log
		tidy(time.Now(), l.days(), l.maxBytes(), l.archiveBytes())
		s.auditMu.Unlock()
		select {
		case <-s.stop:
			return
		case <-tick.C:
		}
	}
}

// tidy applies the limits to the sessions folder as of now.
func tidy(now time.Time, days int, plainMax, gzipMax int64) {
	dir, err := sessionsDir()
	if err != nil {
		return
	}
	var plain, zipped []logFile
	for _, f := range logFiles(dir) {
		switch {
		case days > 0 && f.mod.Before(now.AddDate(0, 0, -days)):
			_ = os.Remove(f.path)
		case strings.HasSuffix(f.path, ".gz"):
			zipped = append(zipped, f)
		default:
			plain = append(plain, f)
		}
	}
	over := total(plain) - plainMax
	for _, f := range plain {
		if over <= 0 {
			break
		}
		if now.Sub(f.mod) < busy {
			continue
		}
		if z, err := gzipFile(f); err == nil {
			over -= f.size
			zipped = append(zipped, z)
		}
	}
	sort.Slice(zipped, func(i, j int) bool { return zipped[i].mod.Before(zipped[j].mod) })
	over = total(zipped) - gzipMax
	for _, f := range zipped {
		if over <= 0 {
			break
		}
		if os.Remove(f.path) == nil {
			over -= f.size
		}
	}
}

func total(fs []logFile) int64 {
	var n int64
	for _, f := range fs {
		n += f.size
	}
	return n
}

// gzipFile replaces a .jsonl with a .jsonl.gz holding the same lines,
// keeping its modification time so it ages the same way. A session that
// continues afterwards starts a new .jsonl next to it; history reads both.
func gzipFile(f logFile) (logFile, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return logFile{}, err
	}
	gz := f.path + ".gz"
	if old, err := readLog(gz); err == nil {
		data = append(old, data...) // an earlier part of the same session
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(data)
	if err := zw.Close(); err != nil {
		return logFile{}, err
	}
	tmp := gz + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return logFile{}, err
	}
	if err := os.Rename(tmp, gz); err != nil {
		return logFile{}, err
	}
	_ = os.Chtimes(gz, f.mod, f.mod)
	_ = os.Remove(f.path)
	return logFile{path: gz, size: int64(buf.Len()), mod: f.mod}, nil
}
