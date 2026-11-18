package app

import (
	"fmt"

	"github.com/Facets-cloud/bx/internal/consent"
	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// cmdConsent lists the sites the user has told bx to always allow storage
// changes on, by browser and profile.
func cmdConsent(e env) error {
	if err := need(e.pos, 0, "bx consent"); err != nil {
		return err
	}
	dir, err := daemon.Dir()
	if err != nil {
		return err
	}
	grants := consent.New(dir).List()
	if e.o.json {
		e.o.printJSON(grants)
		return nil
	}
	if len(grants) == 0 {
		fmt.Fprintln(e.o.w, "no saved storage-change allowances. bx asks before every change.")
		return nil
	}
	var rows [][]string
	for _, g := range grants {
		rows = append(rows, []string{g.Browser, g.Profile, g.Site, g.GrantedAt.Local().Format("2006-01-02 15:04")})
	}
	e.o.table([]string{"BROWSER", "PROFILE", "SITE", "SINCE"}, rows)
	return nil
}

// cmdConsentForget drops every saved allowance for a site.
func cmdConsentForget(e env) error {
	if err := need(e.pos, 1, "bx consent forget <site>"); err != nil {
		return err
	}
	dir, err := daemon.Dir()
	if err != nil {
		return err
	}
	n, err := consent.New(dir).Forget(e.pos[0])
	if err != nil {
		return err
	}
	if n == 0 {
		return exit.New(exit.User, "no saved allowance for %q", e.pos[0])
	}
	fmt.Fprintf(e.o.w, "forgot %d allowance(s) for %s; bx will ask again.\n", n, e.pos[0])
	return nil
}
