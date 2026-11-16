package daemon

import (
	"sync"

	"github.com/Facets-cloud/bx/internal/consent"
)

// capStorage is the capability a storage change needs; it lives here so
// the one call site (storagechange.go) need not import the consent package.
const capStorage = consent.CapStorage

// consentStore remembers the sites the user has allowed storage changes on,
// so bx asks once per site rather than on every change. It is built lazily
// from Dir() and shared by every session on the daemon.
var (
	consentOnce  sync.Once
	consentStore *consent.Store
)

func sites() *consent.Store {
	consentOnce.Do(func() {
		dir, _ := Dir()
		consentStore = consent.New(dir)
	})
	return consentStore
}

// consentFor allows a change on a site, asking the user only when the site
// is not already remembered for this capability. A click on "Always allow"
// is written so bx won't ask for this site again; "Allow" lets this one
// change through without remembering it. A denial or timeout returns the
// error from approve, so the change does not happen.
func consentFor(task, what, site, cap string) error {
	store := sites()
	if store.Remembered(site, cap) {
		return nil
	}
	remember, err := approve(task, what)
	if err != nil {
		return err
	}
	if remember {
		_ = store.Remember(site, cap, consent.ScopeAlways)
	}
	return nil
}
