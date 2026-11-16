// Package consent remembers which sites the user has allowed an agent to
// change storage on, so bx asks once per site instead of on every change.
// A grant is scoped to one site (the host shown in the approval dialog)
// and one capability. "session" grants live only in memory, for the life
// of the daemon; "always" grants are written to <dir>/consent.json and
// outlast it. Nothing a page can reach writes here, so a site can't grant
// itself; only a click in the approval dialog (see approve) does.
package consent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CapStorage is the one capability bx gates today: changing what a site
// keeps in the user's browser (cookies, local or session storage). The
// field lets a later gated operation share this store without a format
// change.
const CapStorage = "storage"

// Scopes. A session grant lives only in memory; an always grant is durable.
const (
	ScopeSession = "session"
	ScopeAlways  = "always"
)

// Grant is one remembered allowance: this site may do this, from GrantedAt.
type Grant struct {
	Site       string    `json:"site"`       // the host, as the dialog shows it (e.g. github.com)
	Capability string    `json:"capability"` // CapStorage
	Scope      string    `json:"scope"`      // session or always
	GrantedAt  time.Time `json:"granted_at"`
}

// Store holds remembered grants. Always grants live in a file under dir;
// session grants live only in memory. The zero Store is not usable: call
// New. A Store is safe for concurrent use.
type Store struct {
	dir     string
	mu      sync.RWMutex
	session map[string]Grant // key: site|capability
}

// New returns a store that keeps its always grants in <dir>/consent.json.
func New(dir string) *Store {
	return &Store{dir: dir, session: map[string]Grant{}}
}

func mapKey(site, cap string) string { return site + "|" + cap }

func (s *Store) file() string { return filepath.Join(s.dir, "consent.json") }

// Remembered reports whether the user has already allowed this capability
// on this site, by a session or an always grant.
func (s *Store) Remembered(site, cap string) bool {
	s.mu.RLock()
	_, ok := s.session[mapKey(site, cap)]
	s.mu.RUnlock()
	if ok {
		return true
	}
	for _, g := range s.load() {
		if g.Site == site && g.Capability == cap {
			return true
		}
	}
	return false
}

// Remember records a grant. A session grant stays in memory; an always
// grant is written to the file so it outlasts the daemon. Remembering a
// site again replaces its earlier grant for the same capability.
func (s *Store) Remember(site, cap, scope string) error {
	g := Grant{Site: site, Capability: cap, Scope: scope, GrantedAt: time.Now().UTC()}
	if scope != ScopeAlways {
		s.mu.Lock()
		s.session[mapKey(site, cap)] = g
		s.mu.Unlock()
		return nil
	}
	grants := s.load()
	for i, e := range grants {
		if e.Site == site && e.Capability == cap {
			grants[i] = g
			return s.write(grants)
		}
	}
	return s.write(append(grants, g))
}

// Forget drops a site's grants: one capability, or all of them when cap is
// empty. It clears both the session and the always grants, so the next
// change on that site asks again.
func (s *Store) Forget(site, cap string) error {
	s.mu.Lock()
	for k := range s.session {
		if k == mapKey(site, cap) || (cap == "" && strings.HasPrefix(k, site+"|")) {
			delete(s.session, k)
		}
	}
	s.mu.Unlock()
	var kept []Grant
	for _, g := range s.load() {
		if g.Site == site && (cap == "" || g.Capability == cap) {
			continue
		}
		kept = append(kept, g)
	}
	return s.write(kept)
}

// List returns every remembered grant, session and always, newest first is
// not guaranteed: the caller sorts if it needs an order.
func (s *Store) List() []Grant {
	s.mu.RLock()
	out := make([]Grant, 0, len(s.session))
	for _, g := range s.session {
		out = append(out, g)
	}
	s.mu.RUnlock()
	return append(out, s.load()...)
}

// load reads the always grants. A missing or unreadable file is no grants:
// the dialog then asks, which is the safe default.
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

// write replaces the always-grants file. It is private to the user (0600)
// under a 0700 dir, like the rest of bx's state.
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
