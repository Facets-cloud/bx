package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Downloaded is a click that saved a file.
type Downloaded struct {
	ActResult
	File  string `json:"file"`
	Bytes int64  `json:"bytes"`
}

// download clicks el and waits for the file it starts to be saved in dir,
// as name if given, else as the name the site suggests. A browser that a
// DevTools client is attached to can drop a page's downloads, so bx
// allows them for this one click, then hands them back to the browser.
func (s *server) download(id, el, dir, name string, timeout time.Duration) (Downloaded, error) {
	dir, err := downloadDir(dir)
	if err != nil {
		return Downloaded{}, err
	}
	info, err := s.findTab(id)
	if err != nil {
		return Downloaded{}, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	w := watchDownload(ctx)
	on := true
	if _, err := chromedp.CallBrowser(ctx, browser.SetDownloadBehavior, browser.SetDownloadBehaviorParams{
		Behavior: browser.SetDownloadBehaviorBehaviorAllowAndName, BrowserContextID: info.BrowserContextID,
		DownloadPath: dir, EventsEnabled: &on}); err != nil {
		if strings.Contains(err.Error(), "browser context") {
			// The browser takes this only for its default profile; other
			// profiles are allowed downloads tab by tab.
			cancel()
			return s.downloadInTab(id, el, dir, name, timeout)
		}
		return Downloaded{}, upstream("allow downloads", err)
	}
	defer func() {
		rctx, rcancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer rcancel()
		_, _ = chromedp.CallBrowser(rctx, browser.SetDownloadBehavior, browser.SetDownloadBehaviorParams{
			Behavior: browser.SetDownloadBehaviorBehaviorDefault, BrowserContextID: info.BrowserContextID})
	}()
	res, err := s.click(id, el, false, timeout)
	if err != nil {
		return Downloaded{ActResult: res}, err
	}
	out := Downloaded{ActResult: res}
	d, err := s.awaitDownload(ctx, id, el, w, min(beginWait, timeout))
	if err != nil {
		return out, err
	}
	if name == "" {
		name = d.suggested
	}
	out.File = freeName(dir, name)
	if err := os.Rename(filepath.Join(dir, d.guid), out.File); err != nil {
		return out, exit.New(exit.User, "saved the download but could not name it %s: %v", out.File, err)
	}
	if st, err := os.Stat(out.File); err == nil {
		out.Bytes = st.Size()
	}
	return out, nil
}

// downloadInTab is download for a tab of a profile other than the
// browser's default one. The browser then can't name the file bx's way or
// report on it, so the file lands in a folder of its own inside dir, and bx
// watches that folder until the download is complete, then moves the file
// into dir.
func (s *server) downloadInTab(id, el, dir, name string, timeout time.Duration) (Downloaded, error) {
	sess, err := s.session(id)
	if err != nil {
		return Downloaded{}, err
	}
	tmp, err := os.MkdirTemp(dir, ".bx-download-")
	if err != nil {
		return Downloaded{}, exit.New(exit.User, "cannot use folder %s: %v", dir, err)
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	if err := setTabDownloads(ctx, sess, "allow", tmp); err != nil {
		return Downloaded{}, upstream("allow downloads", err)
	}
	defer func() {
		rctx, rcancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer rcancel()
		_ = setTabDownloads(rctx, sess, "default", "")
	}()
	res, err := s.click(id, el, false, timeout)
	out := Downloaded{ActResult: res}
	if err != nil {
		return out, err
	}
	saved, err := s.awaitFile(ctx, id, el, tmp, min(beginWait, timeout))
	if err != nil {
		return out, err
	}
	if name == "" {
		name = filepath.Base(saved)
	}
	out.File = freeName(dir, name)
	if err := os.Rename(saved, out.File); err != nil {
		return out, exit.New(exit.User, "saved the download but could not name it %s: %v", out.File, err)
	}
	if st, err := os.Stat(out.File); err == nil {
		out.Bytes = st.Size()
	}
	return out, nil
}

func setTabDownloads(ctx context.Context, sess *tabSession, behavior, dir string) error {
	p := map[string]string{"behavior": behavior}
	if dir != "" {
		p["downloadPath"] = dir
	}
	raw, _ := json.Marshal(p)
	return sess.t.Call(ctx, "Page.setDownloadBehavior", json.RawMessage(raw), nil)
}

// awaitFile waits for a download to appear in tmp and finish: a file is
// there and no partial (.crdownload) one is left. As with awaitDownload,
// when nothing starts within begin it reports what the page shows instead.
func (s *server) awaitFile(ctx context.Context, id, el, tmp string, begin time.Duration) (string, error) {
	started := time.Now()
	for {
		file, partial := folderState(tmp)
		switch {
		case file != "" && !partial:
			return file, nil
		case file == "" && !partial && time.Since(started) > begin:
			return "", s.noDownload(id, el)
		}
		select {
		case <-ctx.Done():
			if file == "" && !partial {
				return "", s.noDownload(id, el)
			}
			return "", exit.New(exit.User, "the download that clicking %s started did not finish in time", el)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// folderState is the finished file in dir, if any, and whether a partial
// download is still there.
func folderState(dir string) (file string, partial bool) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".crdownload") {
			partial = true
		} else if !e.IsDir() {
			file = filepath.Join(dir, e.Name())
		}
	}
	return file, partial
}

// downloadDir is the folder downloads go to, ~/Downloads unless given,
// created if missing.
func downloadDir(dir string) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, "Downloads")
	}
	dir, err := filepath.Abs(dir)
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return "", exit.New(exit.User, "cannot use folder %s: %v", dir, err)
	}
	return dir, nil
}

// beginWait is how long a click has to start a download before bx stops
// waiting and reports what the page showed instead.
const beginWait = 10 * time.Second

// awaitDownload waits for the download a click started to be saved. When
// none starts, the page most likely asks for something first, such as a
// billing address or a sign-in: the error lists what appeared, so the
// agent can ask the user for it instead of waiting out the timeout.
func (s *server) awaitDownload(ctx context.Context, id, el string, w downloadWatch, begin time.Duration) (savedFile, error) {
	t := time.NewTimer(begin)
	defer t.Stop()
	select {
	case <-w.began:
	case <-t.C:
		return savedFile{}, s.noDownload(id, el)
	case <-ctx.Done():
		return savedFile{}, s.noDownload(id, el)
	}
	select {
	case d := <-w.saved:
		return d, d.err
	case <-ctx.Done():
		return savedFile{}, exit.New(exit.User, "the download that clicking %s started did not finish in time", el)
	}
}

// noDownload says a click started no download, and what the page shows
// instead: the dialog that opened, with its fields, or else what changed.
func (s *server) noDownload(id, el string) error {
	msg := fmt.Sprintf("clicking %s started no download", el)
	var d struct {
		Text   string   `json:"text"`
		Fields []string `json:"fields"`
	}
	raw, err := s.eval(id, openDialogJS, "", 5*time.Second)
	if err == nil && json.Unmarshal(raw, &d) == nil && d.Text != "" {
		msg += fmt.Sprintf("; a dialog opened instead: %q", d.Text)
		if len(d.Fields) > 0 {
			msg += "\nits fields: " + strings.Join(d.Fields, ", ")
		}
	} else if ch := s.changes(id); len(ch) > 0 {
		if len(ch) > 30 {
			ch = append(ch[:30], "…")
		}
		msg += "; the page changed instead:\n" + strings.Join(ch, "\n")
	}
	return exit.New(exit.User, "%s\nIf the page asks for something, such as a billing address, a sign-in or a choice, "+
		"ask the user for it (never make it up), fill it in, then download again.", msg)
}

// openDialogJS finds the dialog open on top of the page and returns its
// text and its fields, each as its label and whether it is filled.
const openDialogJS = `() => {
	const shown = e => e.getClientRects().length > 0;
	const d = [...document.querySelectorAll('[role=dialog],[role=alertdialog],dialog[open],[aria-modal=true]')].filter(shown).pop();
	if (!d) return null;
	const fields = [...d.querySelectorAll('input,select,textarea')].filter(f => f.type !== 'hidden' && shown(f))
		.map(f => (f.getAttribute('aria-label') || f.placeholder || f.name || f.type) + (f.value ? ' (filled)' : ' (empty)'));
	return {text: d.innerText.replace(/\s+/g, ' ').trim().slice(0, 300), fields};
}`

// savedFile is how the first download after a click ended: saved under
// its guid, or failed.
type savedFile struct {
	guid, suggested string
	err             error
}

// downloadWatch reports the first download that begins: began closes when
// it starts, and saved gets how it ended.
type downloadWatch struct {
	began chan struct{}
	saved chan savedFile
}

// watchDownload watches for the first download that begins while ctx lasts.
func watchDownload(ctx context.Context) downloadWatch {
	w := downloadWatch{began: make(chan struct{}), saved: make(chan savedFile, 1)}
	var guid, suggested string
	sent := false
	_ = chromedp.ListenBrowser(ctx, func(ev any) {
		switch e := ev.(type) {
		case *browser.EventDownloadWillBegin:
			if guid == "" {
				guid, suggested = e.GUID, e.SuggestedFilename
				close(w.began)
			}
		case *browser.EventDownloadProgress:
			if sent || e.GUID != guid {
				return
			}
			switch e.State {
			case browser.DownloadProgressStateCompleted:
				w.saved <- savedFile{guid: guid, suggested: suggested}
				sent = true
			case browser.DownloadProgressStateCanceled:
				w.saved <- savedFile{err: exit.New(exit.Upstream, "the browser canceled the download of %s", suggested)}
				sent = true
			}
		}
	})
	return w
}

// freeName is a path in dir for name that no file has yet: a page or an
// agent picks the name, never the folder, and nothing is overwritten.
func freeName(dir, name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "download"
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	p := filepath.Join(dir, name)
	for i := 1; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}
