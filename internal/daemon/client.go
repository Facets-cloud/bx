// Package daemon holds one DevTools connection per browser in a background
// process, so the browser asks the user to Allow it once, not per command.
// The CLI talks to it over a Unix socket: one JSON request per connection,
// one JSON response back.
package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Request is one call from the CLI to the daemon.
type Request struct {
	Op   string            `json:"op"`
	Args map[string]string `json:"args,omitempty"`
}

// Response is the daemon's answer. Code is an exit code when Error is set.
type Response struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
	Code  int             `json:"code,omitempty"`
}

// connectWait bounds how long a starting daemon may wait for the user to
// click Allow in the browser.
const connectWait = 2 * time.Minute

// Dir is where sockets, locks and logs live. It is private to the user,
// because the socket gives full control of the browser.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "bx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, 0o700)
}

func paths(browser string) (sock, lock, log string, err error) {
	dir, err := Dir()
	if err != nil {
		return "", "", "", err
	}
	p := filepath.Join(dir, browser)
	return p + ".sock", p + ".lock", p + ".log", nil
}

// Call sends req to the browser's daemon and decodes the response data
// into out (when non-nil). With start set, it starts the daemon first if
// none is running.
func Call(browser string, req Request, out any, start bool) error {
	if os.Getenv("BX_FOLLOW") == "1" {
		// Bring each tab bx works in to the front, so the user can watch.
		if req.Args == nil {
			req.Args = map[string]string{}
		}
		req.Args["follow"] = "true"
	}
	sock, lock, log, err := paths(browser)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		if !start {
			return exit.New(exit.User, "no bx daemon is running for %s", browser)
		}
		if conn, err = startAndDial(browser, sock, lock, log); err != nil {
			return err
		}
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return exit.New(exit.Upstream, "send to daemon: %v", err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return exit.New(exit.Upstream, "daemon closed the connection: %v (see %s)", err, log)
	}
	if strings.HasPrefix(resp.Error, "unknown daemon op") {
		// The running daemon is an older bx, kept alive across an upgrade
		// so the browser does not ask to Allow again.
		return exit.New(exit.User, "the running bx connection is older than this bx and lacks %q: run bx stop (the browser will ask to Allow once more)", req.Op)
	}
	if resp.Error != "" {
		return &exit.Error{Code: resp.Code, Msg: resp.Error}
	}
	if out != nil && len(resp.Data) > 0 {
		return json.Unmarshal(resp.Data, out)
	}
	return nil
}

// Running reports whether a daemon answers on the browser's socket.
func Running(browser string) bool {
	sock, _, _, err := paths(browser)
	if err != nil {
		return false
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// startAndDial starts a daemon under an exclusive lock, so two commands
// that start at once do not start two daemons (and two Allow prompts).
func startAndDial(browser, sock, lock, log string) (net.Conn, error) {
	lf, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	// Another command may have started it while we waited for the lock.
	if conn, err := net.Dial("unix", sock); err == nil {
		return conn, nil
	}
	if err := spawn(browser, log); err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, exit.New(exit.Upstream, "daemon started but its socket does not answer: %v (see %s)", err, log)
	}
	return conn, nil
}

// spawn runs `bx __daemon` detached and waits for its one-line handshake on
// stdout: {} once connected and listening, or an error with a code.
func spawn(browser, log string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()

	cmd := exec.Command(self, "__daemon", "--browser", browser)
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return exit.New(exit.Upstream, "start daemon: %v", err)
	}
	defer cmd.Process.Release()

	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(stdout).ReadString('\n')
		line <- s
	}()
	select {
	case s := <-line:
		return handshake(s, log)
	case <-time.After(connectWait + 10*time.Second):
		_ = cmd.Process.Kill()
		return exit.New(exit.Permission, "timed out waiting for the browser to allow the connection")
	}
}

func handshake(s, log string) error {
	var r Response
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return exit.New(exit.Upstream, "daemon failed to start (see %s)", log)
	}
	if r.Error != "" {
		return &exit.Error{Code: r.Code, Msg: r.Error}
	}
	return nil
}

// errorResponse turns err into a Response carrying its exit code.
func errorResponse(err error) Response {
	var e *exit.Error
	if errors.As(err, &e) {
		return Response{Error: e.Msg, Code: e.Code}
	}
	return Response{Error: fmt.Sprint(err), Code: exit.Upstream}
}
