package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/Facets-cloud/bx/internal/exit"
)

// recorder captures what bx does as video frames: Chromium's screencast of
// the tab bx is working in, following bx from tab to tab. Unlike a
// screenshot it includes the on-page pointer, so a recording shows the
// session the way a person watching would have seen it.
type recorder struct {
	dir   string
	start time.Time
	blur  string // CSS selectors to blur on the recorded pages, if any

	mu     sync.Mutex
	frames []frameFile
	tab    target.ID
	stopFn func() // stops the screencast of tab
}

type frameFile struct {
	name string
	at   time.Duration // since start
}

// Recording is what `bx record stop` returns: where the frames are, and a
// concat list that ffmpeg turns into a video with the real timing.
type Recording struct {
	Dir     string  `json:"dir"`
	List    string  `json:"list"`
	Frames  int     `json:"frames"`
	Seconds float64 `json:"seconds"`
}

func (s *server) recordStart(dir, blur string) (any, error) {
	if dir == "" {
		return nil, exit.New(exit.Validation, "record start needs a folder for the frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, exit.New(exit.User, "create %s: %v", dir, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rec != nil {
		return nil, exit.New(exit.User, "already recording into %s: run bx record stop first", s.rec.dir)
	}
	s.rec = &recorder{dir: dir, start: time.Now(), blur: blur}
	return map[string]string{"dir": dir}, nil
}

// recordFollow points the recording at sess, if a recording is running
// and it isn't already on that tab.
func (s *server) recordFollow(sess *tabSession) {
	s.mu.Lock()
	rec := s.rec
	s.mu.Unlock()
	if rec == nil {
		return
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.tab == sess.id {
		return
	}
	if rec.stopFn != nil {
		rec.stopFn()
	}
	rec.tab = sess.id
	rec.stopFn = rec.capture(sess)
}

// capture starts the screencast of sess and saves each frame; the returned
// function stops it.
func (rec *recorder) capture(sess *tabSession) func() {
	ctx, cancel := context.WithCancel(sess.ctx)
	if rec.blur != "" {
		// Blur first, on this page and on any it loads later, then capture.
		q, _ := json.Marshal(rec.blur)
		script := fmt.Sprintf(blurJS, q)
		_, _ = cdp.Call(ctx, sess.t, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: script})
		_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](script))
		// Wait for the page to paint with the blur before the first frame:
		// otherwise the screencast can send one frame from before it.
		pctx, pcancel := context.WithTimeout(ctx, time.Second) // a covered window may never paint
		_, _ = chromedp.Run(pctx, chromedp.Evaluate[chromedp.Void](
			`new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))`, chromedp.EvalAwaitPromise))
		pcancel()
	}
	frames := cdp.Events(ctx, sess.t, page.ScreencastFrame)
	go func() {
		for ev, err := range frames {
			if err != nil {
				return
			}
			rec.save(ev.Data)
			_, _ = cdp.Call(ctx, sess.t, page.ScreencastFrameAck, page.ScreencastFrameAckParams{SessionID: ev.SessionID})
		}
	}()
	q := int64(85)
	_, _ = cdp.Call(ctx, sess.t, page.StartScreencast, page.StartScreencastParams{
		Format: page.StartScreencastFormatJpeg, Quality: &q, MaxWidth: 1920, MaxHeight: 1200, EveryNthFrame: 1})
	return func() {
		sctx, scancel := context.WithTimeout(sess.ctx, 2*time.Second)
		_, _ = cdp.Call(sctx, sess.t, page.StopScreencast, cdp.Empty{})
		scancel()
		cancel()
	}
}

func (rec *recorder) save(jpeg []byte) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	name := fmt.Sprintf("f%06d.jpg", len(rec.frames))
	if os.WriteFile(filepath.Join(rec.dir, name), jpeg, 0o644) == nil {
		rec.frames = append(rec.frames, frameFile{name: name, at: time.Since(rec.start)})
	}
}

func (s *server) recordStop() (Recording, error) {
	s.mu.Lock()
	rec := s.rec
	s.rec = nil
	s.mu.Unlock()
	if rec == nil {
		return Recording{}, exit.New(exit.User, "not recording: run bx record start first")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.stopFn != nil {
		rec.stopFn()
	}
	end := time.Since(rec.start)
	if len(rec.frames) == 0 {
		return Recording{}, exit.New(exit.User, "the recording has no frames: bx captures a tab once a command acts on it, and the tab must be in front")
	}
	// Each frame lasts until the next one arrives; the screencast sends a
	// frame only when the page changes.
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for i, f := range rec.frames {
		next := end
		if i+1 < len(rec.frames) {
			next = rec.frames[i+1].at
		}
		fmt.Fprintf(&b, "file '%s'\nduration %.3f\n", f.name, (next - f.at).Seconds())
	}
	// The concat demuxer drops the last duration unless the file repeats.
	fmt.Fprintf(&b, "file '%s'\n", rec.frames[len(rec.frames)-1].name)
	list := filepath.Join(rec.dir, "frames.txt")
	if err := os.WriteFile(list, []byte(b.String()), 0o644); err != nil {
		return Recording{}, exit.New(exit.User, "write %s: %v", list, err)
	}
	return Recording{Dir: rec.dir, List: list, Frames: len(rec.frames), Seconds: end.Seconds()}, nil
}

// blurJS blurs every element matching a CSS selector list, for recordings
// of pages with private data. It uses a constructed stylesheet, which
// pages with a strict Content Security Policy still accept, and leaves the
// selectors where the on-page pointer finds them, so its tag doesn't read
// out a blurred element's name.
const blurJS = `((sel) => {
	const k = Symbol.for("bx.blur");
	if (window[k]) return;
	const sheet = new CSSStyleSheet();
	sheet.replaceSync(sel + " { filter: blur(8px) !important; }");
	document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
	window[k] = sheet;
	window[Symbol.for("bx.blurSel")] = sel;
})(%s)`
