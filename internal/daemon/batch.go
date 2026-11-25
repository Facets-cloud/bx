package daemon

import (
	"encoding/json"
	"fmt"
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
	// Input names the pin input that a fill or type step takes its value
	// from (see daemon.Pin). bx run sets Value or Text from it.
	Input string `json:"input,omitempty"`
}

// failureStep is the failed step as a report shows it: as recordStep keeps
// it, plus the input slot it reads (so the agent knows which value to fix).
func failureStep(st Step, names map[string]string) Step {
	rec := recordStep(st, names)
	rec.Input = st.Input
	return rec
}

// recordStep is st as the log and a pin keep it: the element by the name an
// agent can use again (names maps refs as of the latest snapshot), a page's
// URL without its query, and no typed value or input slot. A wait's Text is
// the text it waits for, so it stays.
func recordStep(st Step, names map[string]string) Step {
	rec := st
	rec.Element = named(st.Element, names)
	rec.Input = ""
	switch st.Action {
	case "fill":
		rec.Value = ""
	case "type":
		rec.Text = ""
	case "goto":
		rec.URL = cleanURL(st.URL)
	}
	return rec
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
	defer sess.step.Store("")
	for i, st := range steps {
		if i > 0 {
			sess.stepPause()
		}
		sess.step.Store(fmt.Sprintf("%d/%d", i+1, len(steps)))
		// Refs are named before the step runs: its action may bring a new
		// snapshot, and the ref it names is from the one before.
		names, _ := sess.named.Load().(map[string]string)
		rec := recordStep(st, names)
		r, err := s.step(id, st, timeout)
		if err != nil {
			failed := failureStep(st, names)
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
