package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Facets-cloud/bx/internal/daemon"
)

// writeSkill writes SKILL.md and a VERSION stamp next to it, so a later
// binary can tell that the installed skill is out of date.
func writeSkill(target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, skillMD, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(target), "VERSION"), []byte(Version+"\n"), 0o644)
}

// maybeRefreshSkill reinstalls the skill wherever it is installed but was
// written by another bx version. It skips harnesses without the skill, so
// removing it keeps it removed, and skips dev builds.
func maybeRefreshSkill(errw *os.File) {
	if Version == "dev" {
		return
	}
	for _, t := range skillTargets() {
		if _, err := os.Stat(t); err != nil {
			continue
		}
		stamp, _ := os.ReadFile(filepath.Join(filepath.Dir(t), "VERSION"))
		if strings.TrimSpace(string(stamp)) == Version {
			continue
		}
		if err := writeSkill(t); err == nil && isTerminal(errw) {
			fmt.Fprintf(errw, "bx: updated the skill at %s to %s\n", t, Version)
		}
	}
}

// newer reports whether version a is later than b. Versions look like
// v1.2.3; anything else (such as "dev") is never newer or older.
func newer(a, b string) bool {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		// Drop a pre-release suffix such as "-rc1".
		p, _, _ = strings.Cut(p, "-")
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// updateCache remembers the last release lookup, so bx asks GitHub at
// most once a day.
type updateCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

func cachePath() (string, error) {
	dir, err := daemon.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "update.json"), nil
}

// cachedLatest returns the latest release, from the cache when it is
// under a day old, else from GitHub (with a short timeout).
func cachedLatest(now time.Time) string {
	path, err := cachePath()
	if err != nil {
		return ""
	}
	var c updateCache
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &c) == nil && now.Sub(c.CheckedAt) < 24*time.Hour {
		return c.Latest
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	latest, err := latestRelease(ctx)
	// Record failures too, so an offline machine is not slowed every time.
	b, _ := json.Marshal(updateCache{CheckedAt: now, Latest: latest})
	_ = os.WriteFile(path, b, 0o600)
	if err != nil {
		return ""
	}
	return latest
}

// maybeNoticeUpdate tells a person at a terminal that a newer bx exists.
// It stays quiet for agents, whose stderr may carry JSON errors.
func maybeNoticeUpdate(errw *os.File) {
	if Version == "dev" || !isTerminal(errw) {
		return
	}
	if latest := cachedLatest(time.Now()); newer(latest, Version) {
		fmt.Fprintf(errw, "bx: %s is available (you have %s): run bx update\n", latest, Version)
	}
}
