package daemon

import (
	"log"
	"sync"

	"github.com/Facets-cloud/bx/internal/consent"
)

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

// consentFor allows a storage change on a site in a given browser+profile,
// asking the user only when that browser+profile+site is not already
// remembered. "Always allow" persists the grant. It returns whether the
// change was allowed by a saved grant (no dialog), so the caller can mark the
// audit entry. An empty site is always asked and never remembered.
func consentFor(task, what, browser, profile, site string) (auto bool, err error) {
	store := sites()
	if store.Remembered(browser, profile, site) {
		return true, nil // allowed by a saved choice; no dialog
	}
	alsoAllows := ""
	if site != "" {
		prof := profile
		if prof == "" {
			prof = "this"
		}
		alsoAllows = "Always allow: let agents change any cookie or storage on " + site +
			" in your " + prof + " profile without asking."
	}
	remember, err := approve(task, what, alsoAllows)
	if err != nil {
		return false, err
	}
	if remember {
		// A failed save is not fatal: the change was allowed, it just won't
		// be remembered, so bx asks again next time. Log it rather than drop it.
		if err := store.Remember(browser, profile, site); err != nil { // no-op for empty site
			log.Printf("could not save the Always-allow choice for %s on %s: %v (bx will ask again next time)", profile, site, err)
		}
	}
	return false, nil
}
