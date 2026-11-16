package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"

	"github.com/Facets-cloud/bx/internal/exit"
)

// StorageChange is a change to what a site stores in the user's browser:
// set, delete or clear, in cookies, local or session storage. These are
// the user's real logins, so every change waits for the user to Allow it
// (see approve).
type StorageChange struct {
	Action string `json:"action"` // set, delete or clear
	Area   string `json:"area"`   // cookies, local or session
	Name   string `json:"name,omitempty"`
	Value  string `json:"value,omitempty"`
}

// Changed says what a storage change did.
type Changed struct {
	Changed string `json:"changed"`
}

func (c StorageChange) check() error {
	switch c.Action {
	case "set", "delete", "clear":
	default:
		return exit.New(exit.Validation, "action must be set, delete or clear, not %q", c.Action)
	}
	switch c.Area {
	case "cookies", "local", "session":
	default:
		return exit.New(exit.Validation, "area must be cookies, local or session, not %q", c.Area)
	}
	if c.Action != "clear" && c.Name == "" {
		return exit.New(exit.Validation, "%s needs a name", c.Action)
	}
	return nil
}

// describe says the change in words for the approval dialog, without the
// value.
func (c StorageChange) describe(site string) string {
	kind := map[string]string{"cookies": "cookie", "local": "localStorage item", "session": "sessionStorage item"}[c.Area]
	switch c.Action {
	case "set":
		return fmt.Sprintf("set the %s %q on %s", kind, c.Name, site)
	case "delete":
		return fmt.Sprintf("delete the %s %q on %s", kind, c.Name, site)
	}
	return fmt.Sprintf("clear every %s of %s", kind, site)
}

// changeStorage makes the change on the tab's site once the user allows it.
func (s *server) changeStorage(id string, c StorageChange, timeout time.Duration) (Changed, error) {
	if err := c.check(); err != nil {
		return Changed{}, err
	}
	sess, err := s.lockSession(id)
	if err != nil {
		return Changed{}, err
	}
	defer sess.mu.Unlock()
	if err := sess.errIfDialog(); err != nil {
		return Changed{}, err
	}
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	var page string
	if err := evalJSON(ctx, sess, "location.href", &page); err != nil {
		return Changed{}, err
	}
	site := host(page)
	what := c.describe(site)
	task, _ := sess.title.Load().(string)
	if err := consentFor(task, what, site, capStorage); err != nil {
		return Changed{}, err
	}
	// The user may take up to a minute to answer: a fresh deadline for the change.
	ctx, cancel = context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	if c.Area == "cookies" {
		err = changeCookies(ctx, sess, c, page)
	} else {
		err = changeWebStorage(ctx, sess, c)
	}
	if err != nil {
		return Changed{}, err
	}
	return Changed{Changed: what}, nil
}

func changeCookies(ctx context.Context, sess *tabSession, c StorageChange, page string) error {
	var err error
	switch c.Action {
	case "set":
		_, err = cdp.Call(ctx, sess.t, network.SetCookie, network.SetCookieParams{Name: c.Name, Value: c.Value, URL: page})
	case "delete":
		_, err = cdp.Call(ctx, sess.t, network.DeleteCookies, network.DeleteCookiesParams{Name: c.Name, URL: page})
	case "clear":
		err = clearCookies(ctx, sess, page)
	}
	if err != nil {
		return upstream(c.Action+" cookies", err)
	}
	return nil
}

// clearCookies deletes every cookie the page's URL gets.
func clearCookies(ctx context.Context, sess *tabSession, page string) error {
	res, err := cdp.Call(ctx, sess.t, network.GetCookies, network.GetCookiesParams{URLs: []string{page}})
	if err != nil {
		return err
	}
	for _, k := range res.Cookies {
		if _, err := cdp.Call(ctx, sess.t, network.DeleteCookies, network.DeleteCookiesParams{Name: k.Name, Domain: k.Domain, Path: k.Path}); err != nil {
			return err
		}
	}
	return nil
}

func changeWebStorage(ctx context.Context, sess *tabSession, c StorageChange) error {
	store := map[string]string{"local": "localStorage", "session": "sessionStorage"}[c.Area]
	args, _ := json.Marshal([]string{c.Name, c.Value})
	var op string
	switch c.Action {
	case "set":
		op = "s.setItem(n, v)"
	case "delete":
		op = "s.removeItem(n)"
	default:
		op = "s.clear()"
	}
	var ok bool
	return evalJSON(ctx, sess, fmt.Sprintf("(([n, v]) => { const s = window.%s; %s; return true; })(%s)", store, op, args), &ok)
}
