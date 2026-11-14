package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Config is the user's bx settings, in <Dir>/config.json. Only the user
// changes what lowers logging (bx config set at a terminal); agents can
// read it all and raise retention (see Settings).
type Config struct {
	Browser string    `json:"browser,omitempty"` // set by bx use
	Log     LogConfig `json:"log"`
}

// LogConfig is how the session logs are written and kept. Zero values mean
// the defaults.
type LogConfig struct {
	Values     string `json:"values,omitempty"`      // on (default), full or off
	Days       *int   `json:"days,omitempty"`        // delete sessions older than this; 0 (the default) keeps them
	Max        string `json:"max,omitempty"`         // plain files may take this much before the oldest are gzipped
	ArchiveMax string `json:"archive_max,omitempty"` // gzipped files may take this much before the oldest are deleted
}

// Defaults for the log settings.
const (
	DefaultLogDays    = 0 // forever: the size limits govern, see retention.go
	DefaultLogMax     = "2GB"
	DefaultArchiveMax = "5GB"
)

func configPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadConfig reads the settings; a missing or unreadable file gives the defaults.
func LoadConfig() Config {
	var c Config
	if p, err := configPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &c)
		}
	}
	return c
}

// SaveConfig writes the settings.
func SaveConfig(c Config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Effective log settings, with defaults filled in.
func (l LogConfig) values() string {
	if l.Values == "" {
		return "on"
	}
	return l.Values
}

func (l LogConfig) days() int {
	if l.Days == nil {
		return DefaultLogDays
	}
	return *l.Days
}

func (l LogConfig) maxBytes() int64 {
	return sizeOr(l.Max, DefaultLogMax)
}

func (l LogConfig) archiveBytes() int64 {
	return sizeOr(l.ArchiveMax, DefaultArchiveMax)
}

func sizeOr(s, def string) int64 {
	if n, err := ParseSize(s); err == nil {
		return n
	}
	n, _ := ParseSize(def)
	return n
}

// ParseSize reads a size such as 500MB, 2GB or 1.5GB (powers of 1024).
func ParseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		n      int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(t, u.suffix) {
			t, mult = strings.TrimSpace(strings.TrimSuffix(t, u.suffix)), u.n
			break
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || f <= 0 {
		return 0, exit.New(exit.Validation, "%q is not a size such as 500MB or 2GB", s)
	}
	return int64(f * float64(mult)), nil
}

// Setting is one log setting as bx config shows it.
type Setting struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Default string `json:"default"`
	About   string `json:"about"`
}

// Settings lists the log settings with their current values.
func (c Config) Settings() []Setting {
	l := c.Log
	return []Setting{
		{"log.values", l.values(), "on", "on: log actions, not what was typed; full: typed values too; off: no log"},
		{"log.days", strconv.Itoa(l.days()), strconv.Itoa(DefaultLogDays), "delete sessions older than this many days; 0 keeps them (the size limits still apply)"},
		{"log.max", orDefault(l.Max, DefaultLogMax), DefaultLogMax, "plain session files may take this much; past it the oldest are gzipped"},
		{"log.archive_max", orDefault(l.ArchiveMax, DefaultArchiveMax), DefaultArchiveMax, "gzipped sessions may take this much; past it the oldest are deleted"},
	}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Set changes one log setting. With fromAgent set, it refuses any change
// that would log less or keep logs for less (an agent must not be able to
// hide its own actions), and changing log.values at all, since full would
// write typed passwords to disk.
func (c *Config) Set(key, value string, fromAgent bool) error {
	l := &c.Log
	lower := func() error {
		if fromAgent {
			return exit.New(exit.Permission, "an agent can't lower logging; ask the user to run: bx config set %s %s", key, value)
		}
		return nil
	}
	switch key {
	case "log.values":
		if value != "on" && value != "full" && value != "off" {
			return exit.New(exit.Validation, "log.values is on, full or off")
		}
		if fromAgent && value != l.values() {
			return exit.New(exit.Permission, "only the user changes log.values; ask them to run: bx config set log.values %s", value)
		}
		l.Values = value
	case "log.days":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return exit.New(exit.Validation, "log.days is a number of days, or 0 to keep logs forever")
		}
		if old := l.days(); old == 0 && n != 0 || n != 0 && n < old {
			if err := lower(); err != nil {
				return err
			}
		}
		l.Days = &n
	case "log.max", "log.archive_max":
		n, err := ParseSize(value)
		if err != nil {
			return err
		}
		cur := &l.Max
		old := l.maxBytes()
		if key == "log.archive_max" {
			cur, old = &l.ArchiveMax, l.archiveBytes()
		}
		if n < old {
			if err := lower(); err != nil {
				return err
			}
		}
		*cur = value
	default:
		return exit.New(exit.Validation, "unknown setting %q (see bx config)", key)
	}
	return nil
}

// FormatSettings is a setting list as a table.
func FormatSettings(ss []Setting) string {
	var b strings.Builder
	for _, s := range ss {
		fmt.Fprintf(&b, "%-16s %-6s (default %s)  %s\n", s.Key, s.Value, s.Default, s.About)
	}
	return b.String()
}
