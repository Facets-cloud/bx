package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Step is one action in a batch. Action names the op; the other fields are
// that op's arguments.
type Step struct {
	Action  string   `json:"action"`
	Element string   `json:"element,omitempty"`
	Value   string   `json:"value,omitempty"`
	Text    string   `json:"text,omitempty"`
	Submit  string   `json:"submit,omitempty"`
	Key     string   `json:"key,omitempty"`
	URL     string   `json:"url,omitempty"`
	Double  bool     `json:"double,omitempty"`
	Gone    bool     `json:"gone,omitempty"`
	Files   []string `json:"files,omitempty"`
	// Unreplayable names a page-changing op a pin cannot repeat (see
	// planSteps); a replay refuses it.
	Unreplayable string `json:"unreplayable,omitempty"`
	// Input names the pin input that a fill or type step takes its value
	// from (see daemon.Pin). bx run sets Value or Text from it.
	Input string `json:"input,omitempty"`
	// ByPlace marks an input a re-pin kept by its place among inputs of its
	// kind, not by its label (see withInputs): a renamed field and one removed
	// with another added look the same, so bx run warns until a later re-pin
	// matches it by its exact label.
	ByPlace bool `json:"by_place,omitempty"`
	// Path is the page's URL path (no query) the step started on, as recorded.
	// A replay stops before the step when the page is not on that path (see
	// pageMismatch). Empty when the page could not be read, or on a step that
	// is not replayed from a pin.
	Path string `json:"path,omitempty"`
}

// stepRecorder turns the steps a request ran into what the log and pins keep.
// label resolves an element ref to its label (see labeller); full is the
// log.values full setting, which keeps URLs whole.
type stepRecorder struct {
	label func(ref string) string
	full  bool
}

// failureStep is the failed step as a report shows it: as recordStep keeps
// it, plus the input slot the step reads, so the agent knows which value to
// fix.
func (r stepRecorder) failureStep(st Step) Step {
	rec := r.recordStep(st)
	if st.Input != "" {
		rec.Input = st.Input
	}
	return rec
}

// recordStep is st as the log and a pin keep it. It is an allowlist: the
// element by its label, the step's options, and nothing typed. A wait keeps
// its Text, the text it waits for. A typed value (Value, Text of a type step,
// upload Files) is never kept; a fill, type or upload step records an input
// slot that SavePin names. A goto URL is kept whole under log.values full,
// and otherwise kept only when it has nothing to strip; a URL that would be
// stripped (query, fragment, user info) becomes the input slot "url".
func (r stepRecorder) recordStep(st Step) Step {
	rec := Step{Action: st.Action, Element: r.label(st.Element), Submit: st.Submit, Key: st.Key,
		Double: st.Double, Gone: st.Gone}
	switch st.Action {
	case "key":
		// A single printable key is typed text: it is an input slot, like a fill.
		if typedKey(st.Key) {
			rec.Key, rec.Input = "", "key"
		}
	case "wait":
		rec.Text = st.Text
	case "goto":
		rec.URL, rec.Input = r.url(st.URL)
	case "fill", "type":
		// the input name is set when the pin is saved (withInputs)
	case "upload":
		rec.Input = "file"
	}
	return rec
}

// typedKey is a key that types one printable character (a letter, a digit,
// a space): what an agent types one key at a time. Named keys (Enter, Tab,
// Control+a) are not typed text.
func typedKey(k string) bool {
	r, n := utf8.DecodeRuneInString(k)
	return n > 0 && n == len(k) && unicode.IsPrint(r)
}

// url is the URL a goto step keeps and its input slot, if any.
func (r stepRecorder) url(raw string) (string, string) {
	// A fragment can carry a token (OAuth implicit flow), and cleanURL keeps it.
	if r.full || cleanURL(raw) == raw && !strings.Contains(raw, "#") {
		return raw, ""
	}
	return "", "url"
}

// BatchResult says how many steps ran and where the tab ended up. On a
// failed step, Error says why and Failed is its index; later steps did not
// run.
type BatchResult struct {
	Done   int        `json:"done"`
	Failed *int       `json:"failed,omitempty"`
	Error  string     `json:"error,omitempty"`
	Last   *ActResult `json:"last,omitempty"`
	// Changes is what the whole batch changed on the page, as ActResult's.
	Changes []string `json:"changes,omitempty"`
	// FailedStep is the step that failed, as the log keeps it.
	FailedStep *Step `json:"failed_step,omitempty"`
	// Recorded is each step that ran, as the log keeps it (see recordStep).
	// Not sent back to the caller: the log and pins read it.
	Recorded []Step `json:"-"`
}

// batch runs steps on one tab in order, each waiting for the page to
// settle as the single actions do, and stops at the first failure or
// dialog. One call replaces a round trip per step.
func (s *server) batch(id, raw string, timeout time.Duration) (BatchResult, error) {
	var steps []Step
	if err := json.Unmarshal([]byte(raw), &steps); err != nil || len(steps) == 0 {
		return BatchResult{}, exit.New(exit.Validation, "batch needs a JSON list of steps such as [{\"action\":\"click\",\"element\":\"e12\"}]")
	}
	return s.runSteps(id, steps, timeout)
}

// runSteps is batch for steps already decoded.
func (s *server) runSteps(id string, steps []Step, timeout time.Duration) (BatchResult, error) {
	var res BatchResult
	sess, err := s.session(id)
	if err != nil {
		return BatchResult{}, err
	}
	full := LoadConfig().Log.values() == "full"
	defer sess.step.Store("")
	for i, st := range steps {
		if i > 0 {
			sess.stepPause()
		}
		sess.step.Store(fmt.Sprintf("%d/%d", i+1, len(steps)))
		// Refs are labelled before the step runs: its action may bring a new
		// snapshot, and the ref it names is from the one before.
		sr := sess.recorder(full)
		rec := sr.recordStep(st)
		// The page is checked before the step acts, against the page it started
		// on. A replay leaves the steps it must not check with no path (see
		// unchecked).
		here := sess.pagePath(timeout)
		if msg := pageMismatch(st.Path, here); msg != "" {
			failed := sr.failureStep(st)
			res.Failed, res.Error, res.FailedStep = &i, msg, &failed
			return res, nil
		}
		rec.Path = here
		r, err := s.step(id, st, timeout)
		if err != nil {
			failed := sr.failureStep(st)
			res.Failed, res.Error, res.FailedStep = &i, err.Error(), &failed
			return res, nil
		}
		res.Recorded = append(res.Recorded, rec)
		res.Done = i + 1
		if r != nil {
			res.Last = r
			if r.Dialog != nil && i < len(steps)-1 {
				res.Error = "stopped: the page opened a dialog"
				return res, nil
			}
		}
	}
	return res, nil
}

// pagePath is the URL path of the tab's page, no query, or "" when the browser
// does not list the tab within wait. It reads the browser's target list, not
// the page, so it answers while a JavaScript dialog is open (Runtime.evaluate
// does not).
func (sess *tabSession) pagePath(wait time.Duration) string {
	ctx, cancel := context.WithTimeout(sess.bctx, wait)
	defer cancel()
	infos, err := chromedp.Targets(ctx)
	if err != nil {
		return ""
	}
	return urlPath(pageURL(infos, sess.id))
}

// pageURL is the URL of the target id in infos, or "" when it is not listed.
func pageURL(infos []*target.Info, id target.ID) string {
	for _, t := range infos {
		if t.TargetID == id {
			return t.URL
		}
	}
	return ""
}

// stampPath gives each planned step the page path it starts on. The page is
// read only when there is a step to stamp.
func stampPath(steps []Step, here func() string) {
	if len(steps) == 0 {
		return
	}
	p := here()
	for i := range steps {
		steps[i].Path = p
	}
}

// urlPath is the path of href, no query: "" for an empty or unparsable href,
// and the scheme and opaque part for one with no path, such as about:blank.
func urlPath(href string) string {
	u, err := url.Parse(href)
	if href == "" || err != nil {
		return ""
	}
	if u.Path == "" && u.Opaque != "" {
		return u.Scheme + ":" + u.Opaque
	}
	return u.Path
}

// unchecked is steps with the page check off where a replay must not make it.
// A goto is never checked: it leaves the page it was recorded on, so the tab
// may start anywhere. After a goto whose URL is an input, no step is checked
// until the next goto with a fixed URL: the user's URL can lead to other
// pages than the recorded one. Pass the pin's whole list, so a run --from N
// still knows about a goto before N.
func unchecked(steps []Step) []Step {
	out := slices.Clone(steps)
	free := false
	for i := range out {
		if out[i].Action == "goto" {
			free = out[i].Input != ""
		}
		if out[i].Action == "goto" || free {
			out[i].Path = ""
		}
	}
	return out
}

// samePage is whether two paths are the same page, a record ID aside: they
// have as many segments, and each pair is equal or both are IDs (see
// recordID), so /orders/123/edit is /orders/456/edit but not /orders/new/edit.
func samePage(a, b string) bool {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] && !(recordID(as[i]) && recordID(bs[i])) {
			return false
		}
	}
	return true
}

var uuidSeg = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var objectIDSeg = regexp.MustCompile(`^[0-9a-fA-F]{24}$`)

// recordID is a path segment that names a record: all digits, a UUID, or a
// 24-hex Mongo ObjectId. A slug or a prefixed ID (cus_123) is not one.
func recordID(seg string) bool {
	return seg != "" && strings.Trim(seg, "0123456789") == "" || uuidSeg.MatchString(seg) || objectIDSeg.MatchString(seg)
}

// pageMismatch is why a replayed step must not run on the page here, or "" if
// it may run: want is the path the step recorded, "" when it recorded none.
func pageMismatch(want, here string) string {
	if want == "" || samePage(want, here) {
		return ""
	}
	if here == "" {
		return fmt.Sprintf("expected page %s, but the page could not be read", want)
	}
	return fmt.Sprintf("expected page %s, on %s", want, here)
}

// step runs one batch step. Wait steps report no ActResult.
func (s *server) step(id string, st Step, timeout time.Duration) (*ActResult, error) {
	var r ActResult
	var err error
	switch st.Action {
	case "click":
		r, err = s.click(id, st.Element, st.Double, timeout)
	case "hover":
		r, err = s.hover(id, st.Element, timeout)
	case "fill":
		r, err = s.fill(id, [][2]string{{st.Element, st.Value}}, timeout)
	case "type":
		r, err = s.typeText(id, st.Element, st.Text, st.Submit, timeout)
	case "key":
		r, err = s.key(id, st.Key, timeout)
	case "goto":
		r, err = s.goTo(id, st.URL, timeout)
	case "upload":
		r, err = s.upload(id, st.Element, st.Files, timeout)
	case "scroll":
		r, err = s.scroll(id, st.Element, timeout)
	case "wait":
		_, err = s.wait(id, st.Text, st.Element, st.Gone, timeout)
		return nil, err
	default:
		return nil, exit.New(exit.Validation, "unknown step action %q: use click, hover, fill, type, key, goto, wait, upload or scroll", st.Action)
	}
	return &r, err
}
