package daemon

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Saved is a file bx wrote.
type Saved struct {
	File  string `json:"file"`
	Bytes int64  `json:"bytes"`
}

// pdf saves the page as a PDF in dir, as name if given, else as the page's
// title. It is for pages that have no download of their own, such as an
// order or a receipt page. An existing file is never replaced.
func (s *server) pdf(id, dir, name string, timeout time.Duration) (Saved, error) {
	dir, err := downloadDir(dir)
	if err != nil {
		return Saved{}, err
	}
	sess, err := s.lockSession(id)
	if err != nil {
		return Saved{}, err
	}
	defer sess.mu.Unlock()
	if err := sess.errIfDialog(); err != nil {
		return Saved{}, err
	}
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](removeIndicatorJS)) // keep the pointer out of the PDF
	if name == "" {
		title, _ := chromedp.Run(ctx, chromedp.Title())
		name = pdfName(title)
	}
	yes := true
	res, err := cdp.Call(ctx, sess.t, page.PrintToPDF, page.PrintToPDFParams{PrintBackground: &yes})
	if err != nil {
		return Saved{}, upstream("save as PDF", err)
	}
	out := Saved{File: freeName(dir, name), Bytes: int64(len(res.Data))}
	if err := os.WriteFile(out.File, res.Data, 0o644); err != nil {
		return Saved{}, err
	}
	sess.say(sess.ctx, "Saved the page as a PDF")
	return out, nil
}

// pdfName turns a page title into a file name ending in .pdf.
func pdfName(title string) string {
	title = strings.TrimSpace(strings.NewReplacer("/", "-", ":", "-", "\\", "-").Replace(title))
	if title == "" {
		title = "page"
	}
	if len(title) > 80 {
		title = title[:80]
	}
	return title + ".pdf"
}
