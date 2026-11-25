package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
