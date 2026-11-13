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
}

// batch runs steps on one tab in order, each waiting for the page to
// settle as the single actions do, and stops at the first failure or
// dialog. One call replaces a round trip per step.
func (s *server) batch(id, raw string, timeout time.Duration) (BatchResult, error) {
	var steps []Step
	if err := json.Unmarshal([]byte(raw), &steps); err != nil || len(steps) == 0 {
		return BatchResult{}, exit.New(exit.Validation, "batch needs a JSON list of steps such as [{\"action\":\"click\",\"element\":\"e12\"}]")
	}
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
		r, err := s.step(id, st, timeout)
		if err != nil {
			res.Failed, res.Error = &i, err.Error()
			return res, nil
		}
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
