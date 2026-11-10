// Package am runs the external commands zen-inbox talks to: agent-manager,
// tmux and zen-bell.
package am

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

const (
	SendTimeout = 30 * time.Second
	Socket      = "agentmgr"
	// SenderRefused is agent-manager's error for --from inside an agent pane.
	SenderRefused = "a sender name is for a caller with no session"
)

// Session is one entry of `agent-manager sessions --json`, as decoded.
type Session = map[string]any

// Sessions is nil when agent-manager could not be asked.
type Sessions []Session

// Client is what the inbox needs from the outside world.
type Client interface {
	Sessions() Sessions
	Send(sessionID, message string) (ok bool, messageID, errText string)
	Popup(sessionID string) bool
	Bell(message string)
}

// Exec is the real Client.
type Exec struct {
	// TmuxEnv is $TMUX of the watcher, for the popup's server.
	TmuxEnv string
}

type result struct {
	code   int
	stdout string
	stderr string
}

var errTimeout = errors.New("timeout")

func run(argv []string) (result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), SendTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// A killed child's own children can hold the pipes open past the timeout.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return result{}, errTimeout
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return result{}, err
	}
	return result{cmd.ProcessState.ExitCode(), out.String(), errb.String()}, nil
}

func decode(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("extra data")
	}
	return v, nil
}

func (Exec) Sessions() Sessions {
	r, err := run([]string{"agent-manager", "sessions", "--json"})
	if err != nil || r.code != 0 {
		return nil
	}
	out := r.stdout
	if out == "" {
		out = "[]"
	}
	v, err := decode(out)
	if err != nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	sessions := Sessions{}
	for _, s := range list {
		if m, ok := s.(map[string]any); ok {
			sessions = append(sessions, m)
		}
	}
	return sessions
}

// Find is the session with that id, or nil.
func Find(sessions Sessions, id string) Session {
	for _, s := range sessions {
		if v, ok := s["id"].(string); ok && v == id {
			return s
		}
	}
	return nil
}

// Status is the session's status, or "gone" when it cannot receive.
func Status(s Session) string {
	if s == nil || !pyfmt.Truthy(s["running"]) || pyfmt.Truthy(s["archived"]) {
		return "gone"
	}
	if st, ok := s["status"].(string); ok && (st == "dead" || st == "errored") {
		return "gone"
	}
	if !pyfmt.Truthy(s["status"]) {
		return "unknown"
	}
	return pyfmt.Str(s["status"])
}

func (e Exec) Send(sessionID, message string) (bool, string, string) {
	if sessions := e.Sessions(); sessions != nil && Status(Find(sessions, sessionID)) == "gone" {
		return false, "", fmt.Sprintf("session %s is gone (dead, archived or missing)", sessionID)
	}
	argv := []string{"agent-manager", "send", sessionID, message, "--from", "zen-inbox"}
	r, err := run(append(argv[:6:6], "--json"))
	if err == nil && r.code != 0 && strings.Contains(r.stderr, SenderRefused) {
		r, err = run(append(argv[:4:4], "--json"))
	}
	switch {
	case errors.Is(err, errTimeout):
		return false, "", fmt.Sprintf("agent-manager send timed out after %ds", int(SendTimeout.Seconds()))
	case err != nil:
		return false, "", "agent-manager not found"
	}
	if r.code != 0 {
		text := r.stderr
		if text == "" {
			text = r.stdout
		}
		if text = pyfmt.Strip(text); text == "" {
			text = fmt.Sprintf("agent-manager send exited %d", r.code)
		}
		return false, "", text
	}
	out := r.stdout
	if out == "" {
		out = "{}"
	}
	v, err := decode(out)
	if err != nil {
		v = map[string]any{}
	}
	data, ok := v.(map[string]any)
	if !ok {
		return true, "", ""
	}
	if pyfmt.Truthy(data["refused"]) || pyfmt.Truthy(data["error"]) {
		if pyfmt.Truthy(data["error"]) {
			return false, "", pyfmt.Str(data["error"])
		}
		return false, "", pyfmt.Str(data["refused"])
	}
	mid, ok := data["message_id"]
	if !ok {
		mid, ok = data["id"]
		if !ok {
			mid = ""
		}
	}
	return true, pyfmt.Str(mid), ""
}

// AttachCommand attaches on agent-manager's own server, whatever $TMUX names.
func AttachCommand(sessionID string) string {
	return fmt.Sprintf("tmux -L %s attach -t %s", Socket, shellQuote("am_"+sessionID))
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r == '_' || r == '@' || r == '%' || r == '+' || r == '=' || r == ':' ||
			r == ',' || r == '.' || r == '/' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// PopupArgv is nil without a tmux server to open the popup on.
func PopupArgv(sessionID, tmuxEnv string) []string {
	socket, _, _ := strings.Cut(tmuxEnv, ",")
	if socket == "" {
		return nil
	}
	return []string{
		"tmux", "-S", socket, "display-popup", "-E", "-w", "90%", "-h", "90%",
		"env -u TMUX " + AttachCommand(sessionID),
	}
}

func (e Exec) Popup(sessionID string) bool {
	argv := PopupArgv(sessionID, e.TmuxEnv)
	if argv == nil {
		return false
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	return cmd.Run() == nil
}

func (Exec) Bell(message string) {
	cmd := exec.Command("zen-bell", "window-attention", "-n", "zen-inbox", "-b", message)
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// DirName is Python's Path(directory).name.
func DirName(dir string) string {
	parts := strings.Split(filepath.ToSlash(dir), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" && parts[i] != "." {
			return parts[i]
		}
	}
	return ""
}

// TmuxEnv is the watcher's $TMUX.
func TmuxEnv() string { return os.Getenv("TMUX") }
