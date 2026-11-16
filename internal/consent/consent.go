// Package consent remembers which (browser, profile, site) an agent may
// change storage on, so bx asks once instead of on every change. A grant is
// scoped to one browser, one profile and one site (the host shown in the
// approval dialog). Grants are written to <dir>/consent.json and outlast the
// daemon. Nothing a page can reach writes here, so a site can't grant itself;
// only a click on "Always allow" in the approval dialog does.
package consent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Grant is one remembered allowance: this browser+profile may change storage
// on this site, from GrantedAt, without asking again.
type Grant struct {
	Browser   string    `json:"browser"`
	Profile   string    `json:"profile"`
	Site      string    `json:"site"`
	GrantedAt time.Time `json:"granted_at"`
}

// Store holds remembered grants in <dir>/consent.json. The zero Store is not
// usable: call New. A Store is safe for concurrent use within one process.
type Store struct {
	dir string
	mu  sync.Mutex
}

// New returns a store backed by <dir>/consent.json.
func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) file() string { return filepath.Join(s.dir, "consent.json") }

// Remembered reports whether this browser+profile+site already has a grant.
// An empty site is never remembered.
func (s *Store) Remembered(browser, profile, site string) bool {
	if site == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.load() {
		if g.Browser == browser && g.Profile == profile && g.Site == site {
			return true
		}
	}
	return false
}

// Remember records a grant. An empty site is refused (never persisted).
// It holds the lock across the read-modify-write so two clicks can't race.
func (s *Store) Remember(browser, profile, site string) error {
	if site == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grants := s.load()
	for i, g := range grants {
		if g.Browser == browser && g.Profile == profile && g.Site == site {
			grants[i].GrantedAt = time.Now().UTC()
			return s.write(grants)
		}
	}
	grants = append(grants, Grant{Browser: browser, Profile: profile, Site: site, GrantedAt: time.Now().UTC()})
	return s.write(grants)
}

// Forget drops every grant for site (across browsers and profiles). It
// reports how many it removed.
func (s *Store) Forget(site string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	grants := s.load()
	kept := grants[:0:0]
	removed := 0
	for _, g := range grants {
		if g.Site == site {
			removed++
			continue
		}
		kept = append(kept, g)
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, s.write(kept)
}

// List returns every grant, sorted by site then profile.
func (s *Store) List() []Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.load()
	sort.Slice(g, func(i, j int) bool {
		if g[i].Site != g[j].Site {
			return g[i].Site < g[j].Site
		}
		if g[i].Browser != g[j].Browser {
			return g[i].Browser < g[j].Browser
		}
		return g[i].Profile < g[j].Profile
	})
	return g
}

// load reads the grants. A missing or unreadable file is no grants: the
// dialog then asks, which is the safe default. Caller holds mu.
func (s *Store) load() []Grant {
	data, err := os.ReadFile(s.file())
	if err != nil {
		return nil
	}
	var grants []Grant
	if json.Unmarshal(data, &grants) != nil {
		return nil
	}
	return grants
}

// write replaces the grants file (0600 under the 0700 dir). Caller holds mu.
func (s *Store) write(grants []Grant) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(grants, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.file(), data, 0o600)
}
