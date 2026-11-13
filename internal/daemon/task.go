package daemon

import (
	"sync"
	"time"

	"github.com/chromedp/cdproto/target"
)

// task is one agent's piece of browser work: a session that opens with the
// agent's first command (or its start call) and closes when it calls done.
// It holds what the agent chose for the task and what it did, across tabs
// and profiles.
type task struct {
	id      string
	mu      sync.Mutex
	title   string
	mode    string // "fast" or "watch"; empty means the request's own
	typing  string // "paste" or "keys"; empty means the request's own
	started time.Time
	actions int
	opened  []target.ID        // tabs the task opened, in order
	touched map[target.ID]bool // tabs the task acted on
}

// TaskSummary is what done returns: what the session did, and the tabs it
// opened, which stay open unless the agent closes them.
type TaskSummary struct {
	Title   string  `json:"title,omitempty"`
	Seconds float64 `json:"seconds"`
	Actions int     `json:"actions"`
	Opened  []Tab   `json:"opened_tabs"`
}

// taskFor returns the session a request belongs to, starting it on first
// use. Requests without a session id have none.
func (s *server) taskFor(id string) *task {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[id]
	if t == nil {
		t = &task{id: id, started: time.Now(), touched: map[target.ID]bool{}}
		s.tasks[id] = t
	}
	return t
}

// start names the session and sets its mode and typing; they override
// what each request carries for the rest of the session.
func (s *server) start(a map[string]string) (any, error) {
	t := s.taskFor(a["session"])
	if t == nil {
		return nil, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.title, t.mode, t.typing = a["title"], a["task_mode"], a["task_typing"]
	return map[string]string{"session": t.id, "title": t.title}, nil
}

// apply sets a tab's pointer and pacing for a request: the session's
// choices when it made them, else the request's.
func (t *task) apply(sess *tabSession, a map[string]string) {
	mode, typing := a["mode"], a["typing"]
	if t != nil {
		t.mu.Lock()
		if t.mode != "" {
			mode = t.mode
		}
		if t.typing != "" {
			typing = t.typing
		}
		t.touched[sess.id] = true
		t.actions++
		sess.title.Store(t.title)
		t.mu.Unlock()
	} else {
		sess.title.Store("")
	}
	sess.fast.Store(mode != "watch")
	sess.keyTyping.Store(typing == "keys")
}

// opened records a tab the session opened.
func (t *task) noteOpened(id target.ID) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.opened = append(t.opened, id)
	t.mu.Unlock()
}

// finish closes the session: its tabs' pointers show Done and the summary,
// then go away. Without a session it tidies every tab bx has drawn on.
func (s *server) finish(id, summary string) (any, error) {
	s.mu.Lock()
	t := s.tasks[id]
	delete(s.tasks, id)
	s.mu.Unlock()
	if t == nil {
		return s.done(summary, nil)
	}
	t.mu.Lock()
	touched, opened := t.touched, t.opened
	sum := TaskSummary{Title: t.title, Seconds: time.Since(t.started).Seconds(), Actions: t.actions, Opened: []Tab{}}
	t.mu.Unlock()
	if _, err := s.done(summary, touched); err != nil {
		return nil, err
	}
	names := s.profileNames()
	for _, id := range opened {
		if info, err := s.findTab(string(id)); err == nil {
			sum.Opened = append(sum.Opened, s.tabInfo(info, names))
		}
	}
	return sum, nil
}
