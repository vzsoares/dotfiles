// Package am runs the external commands zen-inbox talks to: agent-manager,
// tmux and zen-bell.
package am

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

const (
	SendTimeout = 30 * time.Second
	Socket      = "agentmgr"
	// SenderRefused is agent-manager's error for --from inside an agent pane.
	SenderRefused = "a sender name is for a caller with no session"
	// Sound is zen-bell's window-attention sound on macOS.
	Sound = "Ping"
)

// Session is one entry of `agent-manager sessions --json`, as decoded.
type Session = map[string]any

// Sessions is nil when agent-manager could not be asked.
type Sessions []Session

// Client is what the inbox needs from the outside world.
type Client interface {
	Sessions() Sessions
	Send(sessionID, message string) (ok bool, messageID, errText string)
	// CanPopup reports whether Popup can open for the session, so the agent is
	// not told about a talk that cannot start.
	CanPopup(sessionID string) error
	Popup(sessionID string) error
	Bell(message string)
	// Notify posts one banner.
	Notify(title, body string)
	// Push sends the content-free ntfy alert, when ZEN_NTFY_TOPIC is set.
	Push(body string)
	// Chief queues message for the live chief session (zen-chief) and returns
	// the message id, or the error text; down means no live session is named
	// chief, so the message can wait for one.
	Chief(message string) (messageID, errText string, down bool)
	// ChiefStatus is agent-manager's status for a message Chief queued.
	ChiefStatus(messageID string) (status, errText string)
	// MessageStatus is agent-manager's status for a message id.
	MessageStatus(messageID string) (status, errText string)
	// Followup runs after a successful Send and returns "", Submitted or StillStuck.
	Followup(sessionID string) string
}

// Exec is the real Client.
type Exec struct {
	// TmuxEnv is $TMUX of the watcher, for the popup's server.
	TmuxEnv string
	// OS overrides runtime.GOOS, for tests.
	OS string
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
	if st, ok := s["status"].(string); ok && st == "dead" {
		return "gone"
	}
	if !pyfmt.Truthy(s["status"]) {
		return "unknown"
	}
	return pyfmt.Str(s["status"])
}

// Live is whether the session can receive now.
func Live(s Session) bool { return Status(s) != "gone" }

// Down is a session that cannot receive now but can come back: listed, not
// archived, and gone.
func Down(s Session) bool { return s != nil && !pyfmt.Truthy(s["archived"]) && !Live(s) }

// LiveNamed are the ids of the live sessions with that name.
func LiveNamed(sessions Sessions, name string) []string {
	var out []string
	for _, s := range sessions {
		id, _ := s["id"].(string)
		if n, _ := s["name"].(string); name != "" && n == name && id != "" && Live(s) {
			out = append(out, id)
		}
	}
	return out
}

// GoneText is the delivery error for a session that cannot receive.
func GoneText(sessionID string) string {
	return fmt.Sprintf("session %s is gone (dead, archived or missing)", sessionID)
}

func (e Exec) Send(sessionID, message string) (bool, string, string) {
	if sessions := e.Sessions(); sessions != nil && !Live(Find(sessions, sessionID)) {
		return false, "", GoneText(sessionID)
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

// chiefDownExit is zen-chief's exit code for "no live session named chief".
const chiefDownExit = 3

// ChiefName is the session zen-chief sends to.
const ChiefName = "chief"

// zenChief runs the zen-chief script, the one place that finds chief; down
// is its chiefDownExit.
func zenChief(args ...string) (data map[string]any, errText string, down bool) {
	r, err := run(append([]string{"zen-chief", "--json"}, args...))
	switch {
	case errors.Is(err, errTimeout):
		return nil, "zen-chief timed out", false
	case err != nil:
		return nil, "zen-chief not found (run ./link-bin)", false
	case r.code != 0:
		text := pyfmt.Strip(strings.TrimPrefix(pyfmt.Strip(r.stderr), "zen-chief: "))
		if text == "" {
			text = fmt.Sprintf("zen-chief exited %d", r.code)
		}
		return nil, text, r.code == chiefDownExit
	}
	v, err := decode(r.stdout)
	data, ok := v.(map[string]any)
	if err != nil || !ok {
		return nil, "zen-chief printed no JSON", false
	}
	return data, "", false
}

func (Exec) Chief(message string) (string, string, bool) {
	data, errText, down := zenChief("--", message)
	if errText != "" {
		return "", errText, down
	}
	id := pyfmt.Str(data["id"])
	if id == "" {
		return "", "zen-chief printed no message id", false
	}
	return id, "", false
}

func (Exec) ChiefStatus(messageID string) (string, string) {
	data, errText, _ := zenChief("--status", messageID)
	if errText != "" {
		return "", errText
	}
	return pyfmt.Str(data["status"]), ""
}

func (Exec) MessageStatus(messageID string) (string, string) {
	r, err := run([]string{"agent-manager", "message-status", messageID, "--json"})
	switch {
	case errors.Is(err, errTimeout):
		return "", "agent-manager message-status timed out"
	case err != nil:
		return "", "agent-manager not found"
	case r.code != 0:
		text := pyfmt.Strip(r.stderr)
		if text == "" {
			text = fmt.Sprintf("agent-manager message-status exited %d", r.code)
		}
		return "", text
	}
	v, err := decode(r.stdout)
	data, ok := v.(map[string]any)
	if err != nil || !ok || !pyfmt.Truthy(data["status"]) {
		return "", "message-status printed no status"
	}
	return pyfmt.Str(data["status"]), ""
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

// PopupArgv is nil without a tmux server to open the popup on. client must
// be a terminal client: display-popup on a control-mode one exits 0 and draws
// nothing.
func PopupArgv(sessionID, socket, client string) []string {
	if socket == "" || client == "" {
		return nil
	}
	return []string{
		"tmux", "-S", socket, "display-popup", "-c", client, "-E", "-w", "90%", "-h", "90%",
		"env -u TMUX " + AttachCommand(sessionID),
	}
}

// DefaultSocket is the tmux server a plain `tmux` in a terminal uses.
func DefaultSocket() string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
}

// PickClient is the most recently active non-control client in `tmux
// list-clients -F '#{client_name}<TAB>#{client_control_mode}<TAB>#{client_activity}'`.
func PickClient(out string) string {
	best, bestAt := "", int64(-1)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) != 3 || f[0] == "" || f[1] != "0" {
			continue
		}
		at, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			at = 0
		}
		if at > bestAt {
			best, bestAt = f[0], at
		}
	}
	return best
}

func listClients(socket string) string {
	out, err := exec.Command("tmux", "-S", socket, "list-clients", "-F",
		"#{client_name}\t#{client_control_mode}\t#{client_activity}").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// popupTarget is the server and terminal client to draw the popup on. A
// panel inside agent-manager's server sees only its control-mode client, so
// the terminal attached to the default server is the fallback.
func (e Exec) popupTarget() (socket, client string, err error) {
	own, _, _ := strings.Cut(e.TmuxEnv, ",")
	if own == "" {
		return "", "", errors.New("not running inside tmux")
	}
	for _, s := range []string{own, DefaultSocket()} {
		if c := PickClient(listClients(s)); c != "" {
			return s, c, nil
		}
	}
	return "", "", errors.New("no terminal client attached to the tmux server")
}

// CanPopup reports why Popup cannot open, nil when it can.
func (e Exec) CanPopup(sessionID string) error {
	if _, _, err := e.popupTarget(); err != nil {
		return err
	}
	if exec.Command("tmux", "-L", Socket, "has-session", "-t", "am_"+sessionID).Run() != nil {
		return errors.New("agent session am_" + sessionID + " is not running")
	}
	return nil
}

// Popup blocks until the popup closes.
func (e Exec) Popup(sessionID string) error {
	socket, client, err := e.popupTarget()
	if err != nil {
		return err
	}
	argv := PopupArgv(sessionID, socket, client)
	var stderr bytes.Buffer
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

func (e Exec) Bell(message string) { e.Notify("zen-inbox", message) }

// Notify posts the banner in the background. macOS posts directly: going
// through zen-bell hides terminal-notifier refusing to post (notifications off
// for it), so a failed post falls back to osascript.
func (e Exec) Notify(title, body string) {
	goos := e.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	if Remote() {
		return
	}
	go notify(goos, title, body)
}

// Remote reports an SSH session, a second panel whose alerts belong to the
// desk cockpit.
func Remote() bool { return os.Getenv("SSH_CONNECTION") != "" }

// pushTimeout bounds one ntfy POST.
var pushTimeout = 5 * time.Second

// Push POSTs body to ZEN_NTFY_URL/ZEN_NTFY_TOPIC. A failure is logged and
// dropped; it runs in a tea.Cmd, so it may block.
func (e Exec) Push(body string) {
	topic := os.Getenv("ZEN_NTFY_TOPIC")
	if topic == "" || Remote() {
		return
	}
	base := os.Getenv("ZEN_NTFY_URL")
	if base == "" {
		base = "https://ntfy.sh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(base, "/")+"/"+topic, strings.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "zen-inbox: ntfy:", err)
		return
	}
	if click := os.Getenv("ZEN_NTFY_CLICK"); click != "" {
		req.Header.Set("Click", click)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zen-inbox: ntfy:", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		fmt.Fprintln(os.Stderr, "zen-inbox: ntfy:", resp.Status)
	}
}

func notify(goos, title, body string) {
	if goos != "darwin" {
		_, _ = run([]string{"zen-bell", "window-attention", "-n", title, "-b", body})
		return
	}
	// terminal-notifier reads a leading "[" as markup.
	msg := body
	if strings.HasPrefix(body, "[") {
		msg = `\` + body
	}
	argv := []string{"terminal-notifier", "-title", title, "-message", msg, "-sound", Sound}
	if id := os.Getenv("__CFBundleIdentifier"); id != "" {
		argv = append(argv, "-activate", id)
	}
	if r, err := run(argv); err == nil && r.code == 0 {
		return
	}
	_, _ = run([]string{
		"osascript",
		"-e", "on run argv",
		"-e", `display notification (item 2 of argv) with title (item 1 of argv) sound name "` + Sound + `"`,
		"-e", "end run",
		"--", title, body,
	})
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

// Transient is whether a send error may pass on its own: agent-manager's
// per-minute limit, a timeout, or the manager not answering. A session that
// is gone or a bad request is not.
func Transient(errText string) bool {
	l := strings.ToLower(errText)
	for _, s := range []string{"too many messages", "timed out", "agent-manager not found",
		"connection refused", "agent-manager is not running"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}
