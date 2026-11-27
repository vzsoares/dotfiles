package inbox

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

const (
	SendLimit  = 8000
	SendMargin = 200
	Header     = "[zen-inbox] Answers to your decisions:"
	NoteHeader = "[zen-inbox] A note on your open decisions:"
	LeftOpen   = "Left open: "
)

var taskRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func checkTask(task string) error {
	if !taskRE.MatchString(task) {
		return errf(2, "bad task %s: letters, digits, - or _", pyfmt.Repr(task))
	}
	return nil
}

// checkOptions keeps Python's `rec not in LETTERS[:n]` substring test.
func checkOptions(options []string, rec string) error {
	if len(options) == 1 || len(options) > len(Letters) {
		return errf(2, "give 0 options (free text) or 2 to 4")
	}
	for _, o := range options {
		if pyfmt.Blank(o) {
			return errf(2, "options must not be empty")
		}
	}
	if rec != "" && !strings.Contains(Letters[:len(options)], rec) {
		return errf(2, "--rec %s is not one of the options", pyfmt.Repr(rec))
	}
	return nil
}

func checkBody(body string) error {
	if len(body) > BodyMax {
		return errf(2, "--body is over %d bytes", BodyMax)
	}
	return nil
}

// shellFences are the info strings whose block is a command; other tags
// (sql, json, ts, diff...) mark an example.
var shellFences = map[string]bool{"": true, "sh": true, "bash": true, "zsh": true, "shell": true, "console": true}

// HasFence is whether body holds a fenced block that is a command: untagged
// or tagged sh, bash, zsh, shell or console.
func HasFence(body string) bool {
	open := ""
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimLeft(line, " \t")
		mark := ""
		for _, m := range []string{"```", "~~~"} {
			if strings.HasPrefix(t, m) {
				mark = m
			}
		}
		switch {
		case mark == "":
		case open == "":
			open = mark
			info := strings.Fields(strings.TrimLeft(t, mark[:1]))
			lang := ""
			if len(info) > 0 {
				lang = strings.ToLower(info[0])
			}
			if shellFences[lang] {
				return true
			}
		case mark == open:
			open = ""
		}
	}
	return false
}

// NeedsRunner is the refusal for a body with a command and no word on who runs it.
const NeedsRunner = "--body has a code block: add --manual (the user runs it) or --agent-runs (you run it after the answer); tag an example's fence with its language"

func (ib *Inbox) insertOptions(id string, options []string) error {
	for i, o := range options {
		if err := ib.exec("INSERT INTO decision_options (decision_id, letter, text) VALUES (?, ?, ?)",
			id, Letters[i:i+1], o); err != nil {
			return err
		}
	}
	return nil
}

// AskInput is one `zen-inbox ask`.
type AskInput struct {
	Task, Question  string
	Body            string
	Options         []string
	Rec, Context    string
	Gate            bool
	SessionID, Name string
	Title, Brief    string
	// Prod binds the ask to one prod action; Question and Options must be
	// ProdQuestion and ProdOptions (AskProd fills them).
	Prod *ProdAction
	// Manual makes it a manual-step ask: Options must be empty.
	Manual bool
	// AgentRuns marks that the asker runs the body's command after the answer.
	AgentRuns bool
}

func (ib *Inbox) Ask(in AskInput) (Decision, error) {
	if err := checkTask(in.Task); err != nil {
		return Decision{}, err
	}
	kind := ""
	if in.Manual && in.AgentRuns {
		return Decision{}, errf(2, "--manual and --agent-runs conflict: say who runs the command")
	}
	if in.AgentRuns && in.Prod != nil {
		return Decision{}, errf(2, "--agent-runs does not apply to a --prod ask")
	}
	if in.AgentRuns {
		kind = KindAgentRuns
	}
	if in.Manual {
		if len(in.Options) > 0 || in.Gate || in.Prod != nil {
			return Decision{}, errf(2, "--manual writes its own options (Done, Later); drop -o, --gate and --prod")
		}
		kind, in.Options = KindManual, ManualOptions
		if in.Rec == "" {
			in.Rec = Letters[:1]
		}
	}
	if pyfmt.Blank(in.Question) {
		return Decision{}, errf(2, "a question is required")
	}
	if err := checkOptions(in.Options, in.Rec); err != nil {
		return Decision{}, err
	}
	if err := checkBody(in.Body); err != nil {
		return Decision{}, err
	}
	if pyfmt.Blank(in.Body) {
		in.Body = ""
	}
	if kind == "" && in.Prod == nil && HasFence(in.Body) {
		return Decision{}, errf(2, "%s", NeedsRunner)
	}
	now := stamp()
	gate := 0
	if in.Gate {
		gate = 1
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var id string
	err := ib.immediate(func() error {
		var seq int64
		if err := ib.conn.QueryRowContext(ctx,
			"SELECT MAX(COALESCE((SELECT MAX(seq) FROM decisions WHERE task = ? AND gate = ?), 0),"+
				" COALESCE((SELECT seq FROM decision_seq WHERE task = ? AND gate = ?), 0))",
			in.Task, gate, in.Task, gate).Scan(&seq); err != nil {
			return err
		}
		seq++
		switch {
		case !in.Gate:
			id = fmt.Sprintf("%s.%d", in.Task, seq)
		case seq == 1:
			id = in.Task + ".g"
		default:
			id = fmt.Sprintf("%s.g%d", in.Task, seq)
		}
		if err := ib.exec("INSERT INTO decisions (id, task, seq, gate, session_id, session_name,"+
			" question, body, context, rec, kind, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			id, in.Task, seq, gate, in.SessionID, in.Name, in.Question, in.Body, in.Context, in.Rec,
			kind, now, now); err != nil {
			return err
		}
		if err := ib.insertOptions(id, in.Options); err != nil {
			return err
		}
		if in.Prod != nil {
			if err := ib.insertProd(id, *in.Prod); err != nil {
				return err
			}
		}
		if in.Title != "" || in.Brief != "" {
			return ib.upsertBrief(in.SessionID, in.Task, nonEmpty(in.Title), nonEmpty(in.Brief))
		}
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	d, err := ib.get(id)
	return d, dbErr(err)
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func bit(b bool) int {
	if b {
		return 1
	}
	return 0
}

// upsertBrief keeps the stored value for a nil field, replaces it otherwise.
func (ib *Inbox) upsertBrief(sid, task string, title, text *string) error {
	return ib.exec("INSERT INTO decision_briefs (session_id, task, title, brief, updated_at)"+
		" VALUES (?, ?, ?, ?, ?) ON CONFLICT (session_id, task) DO UPDATE SET"+
		" title = CASE WHEN ? THEN excluded.title ELSE title END,"+
		" brief = CASE WHEN ? THEN excluded.brief ELSE brief END,"+
		" updated_at = excluded.updated_at",
		sid, task, deref(title), deref(text), stamp(), bit(title != nil), bit(text != nil))
}

// SetBrief returns the stored row as strings, in column order.
func (ib *Inbox) SetBrief(sid, task string, title, text *string) (pyfmt.Object, error) {
	if err := checkTask(task); err != nil {
		return nil, err
	}
	if title == nil && text == nil {
		return nil, errf(2, "give --title or --text")
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var row [5]string
	err := ib.immediate(func() error {
		if err := ib.upsertBrief(sid, task, title, text); err != nil {
			return err
		}
		return ib.conn.QueryRowContext(ctx, "SELECT CAST(session_id AS TEXT), CAST(task AS TEXT),"+
			" CAST(title AS TEXT), CAST(brief AS TEXT), CAST(updated_at AS TEXT)"+
			" FROM decision_briefs WHERE session_id = ? AND task = ?", sid, task).
			Scan(&row[0], &row[1], &row[2], &row[3], &row[4])
	})
	if err != nil {
		return nil, err
	}
	keys := []string{"session_id", "task", "title", "brief", "updated_at"}
	out := make(pyfmt.Object, len(keys))
	for i, k := range keys {
		out[i] = pyfmt.KV{Key: k, Value: row[i]}
	}
	return out, nil
}

func (ib *Inbox) openOwned(id, caller string, force bool) (Decision, error) {
	d, err := ib.get(id)
	if err != nil {
		return d, err
	}
	if d.Status != "open" {
		return d, errf(1, "%s is %s", id, d.Status)
	}
	if !force && caller != d.SessionID {
		return d, errf(1, "%s belongs to session %s; --force overrides", id, d.SessionID)
	}
	return d, nil
}

// AmendInput fields left nil keep the stored value.
type AmendInput struct {
	Question *string
	Options  []string
	Rec      *string
	Context  *string
	Body     *string
}

func (ib *Inbox) Amend(id, caller string, force bool, in AmendInput) (Decision, error) {
	if in.Question == nil && in.Options == nil && in.Rec == nil && in.Context == nil && in.Body == nil {
		return Decision{}, errf(2, "nothing to amend")
	}
	if in.Question != nil && pyfmt.Blank(*in.Question) {
		return Decision{}, errf(2, "a question is required")
	}
	if in.Body != nil {
		if err := checkBody(*in.Body); err != nil {
			return Decision{}, err
		}
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	err := ib.immediate(func() error {
		d, err := ib.openOwned(id, caller, force)
		if err != nil {
			return err
		}
		if d.Prod != nil {
			return errf(1, "%s is a prod-action ask; withdraw and re-ask", id)
		}
		if d.Kind == KindManual && in.Options != nil {
			return errf(1, "%s is a manual-step ask; its options are fixed", id)
		}
		texts := in.Options
		if texts == nil {
			for _, o := range d.Options {
				texts = append(texts, o.Text)
			}
		}
		rec := d.Rec
		if in.Rec != nil {
			rec = *in.Rec
		}
		if in.Options != nil && in.Rec == nil && !strings.Contains(Letters[:min(len(texts), len(Letters))], rec) {
			rec = ""
		}
		if err := checkOptions(texts, rec); err != nil {
			return err
		}
		question, context, body := d.Question, d.Context, d.Body
		if in.Body != nil {
			body = *in.Body
			if pyfmt.Blank(body) {
				body = ""
			}
		}
		if in.Question != nil {
			question = *in.Question
		}
		if in.Context != nil {
			context = *in.Context
		}
		confirmed := d.ConfirmedAt
		if d.Kind == KindAgentRuns && body != d.Body {
			confirmed = ""
		}
		if d.Kind == "" && in.Body != nil && HasFence(body) {
			return errf(2, "%s", NeedsRunner)
		}
		if err := ib.exec("UPDATE decisions SET question = ?, body = ?, context = ?, rec = ?, amended = 1,"+
			" confirmed_at = ?, updated_at = ?, rev = rev + 1 WHERE id = ?",
			question, body, context, rec, confirmed, stamp(), id); err != nil {
			return err
		}
		if in.Options != nil {
			if err := ib.exec("DELETE FROM decision_options WHERE decision_id = ?", id); err != nil {
				return err
			}
			return ib.insertOptions(id, texts)
		}
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	d, err := ib.get(id)
	return d, dbErr(err)
}

type closeArgs struct {
	status, kind, option, text, note string
	delivery                         string
	caller                           string
	owned                            bool
	rev                              *int64
}

// closeOne is the guarded open → closed transition; it returns the row as it
// was before closing.
func (ib *Inbox) closeOne(id string, a closeArgs) (Decision, error) {
	now := stamp()
	if a.delivery == "" {
		a.delivery = "none"
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var d Decision
	err := ib.immediate(func() error {
		var err error
		if d, err = ib.openOwned(id, a.caller, !a.owned); err != nil {
			return err
		}
		if a.rev != nil && d.Rev != *a.rev {
			return &Error{Msg: id + " was amended", Code: 1, Amended: true}
		}
		if a.status == "answered" && !ib.User && d.Prod != nil {
			return errf(1, "%s is a prod-action ask; answer it in the decisions panel", id)
		}
		if a.kind == "pick" && d.OptionText(a.option) == "" {
			return errf(2, "%s has no option %s", id, pyfmt.Repr(a.option))
		}
		confirmed := ""
		if d.Kind == KindAgentRuns {
			confirmed = d.ConfirmedAt
		}
		if a.status == "answered" && a.kind == "pick" && d.ManualDone(a.option) {
			if !ib.User {
				return errf(1, "%s is a manual-step ask; confirm Done in the decisions panel", id)
			}
			confirmed = now
		}
		if err := ib.deleteDraft(id); err != nil {
			return err
		}
		return ib.exec("UPDATE decisions SET status = ?, answer_kind = ?, answer_option = ?,"+
			" answer_text = ?, note = ?, delivery = ?, answered_at = ?, confirmed_at = ?,"+
			" updated_at = ?, rev = rev + 1 WHERE id = ? AND status = 'open'",
			a.status, a.kind, a.option, a.text, a.note, a.delivery, now, confirmed, now, id)
	})
	return d, err
}

// CloseRaw is the bare transition, for fixtures that need a held answer
// without the flush that Answer would run.
func (ib *Inbox) CloseRaw(id, status, kind, option, text, delivery string) error {
	_, err := ib.closeOne(id, closeArgs{status: status, kind: kind, option: option, text: text, delivery: delivery})
	return err
}

// Withdraw closes a decision unanswered. When the asking session withdraws
// its own last open decision, the flush would run inside that session, where
// --from is refused and the fallback messages the agent itself (#582), so
// the held answers are handed back in Delivery.Message instead of sent.
func (ib *Inbox) Withdraw(id, caller string, force bool) (*Delivery, error) {
	d, err := ib.closeOne(id, closeArgs{status: "withdrawn", caller: caller, owned: !force})
	if err != nil {
		return nil, err
	}
	return ib.flush(d.SessionID, d.Task, caller == d.SessionID)
}

// Answer records a pick (letter) or free text; rev, when given, refuses the
// answer as Amended if the decision changed since it was read.
func (ib *Inbox) Answer(id string, letter, text *string, note string, rev *int64) (*Delivery, error) {
	d, err := ib.Record(id, letter, text, note, rev)
	if err != nil {
		return nil, err
	}
	return ib.Flush(d.SessionID, d.Task)
}

// Record stores an answer as held without sending it; FlushSession sends the
// session's held answers together.
func (ib *Inbox) Record(id string, letter, text *string, note string, rev *int64) (Decision, error) {
	if (letter == nil) == (text == nil) {
		return Decision{}, errf(2, "give an option letter or --text, not both")
	}
	if text != nil && pyfmt.Blank(*text) {
		return Decision{}, errf(2, "--text must not be empty")
	}
	kind := "pick"
	if letter == nil {
		kind = "text"
	}
	return ib.closeOne(id, closeArgs{status: "answered", kind: kind, option: deref(letter),
		text: deref(text), note: note, delivery: "held", rev: rev})
}

func (ib *Inbox) Resolve(id, note string) (*Delivery, error) {
	d, err := ib.closeOne(id, closeArgs{status: "answered", kind: "talk", note: note})
	if err != nil {
		return nil, err
	}
	return ib.Flush(d.SessionID, d.Task)
}

func (ib *Inbox) claim(ids []string) error {
	now := stamp()
	for _, id := range ids {
		if err := ib.exec("UPDATE decisions SET delivery = 'sending', updated_at = ?, rev = rev + 1"+
			" WHERE id = ?", now, id); err != nil {
			return err
		}
	}
	return nil
}

// target is where a delivery goes: a session id, or none with the reason.
// down means the session can come back, so the delivery waits for it.
type target struct {
	id, reason string
	down       bool
}

// answerTarget is the asking session while it is live, else the one live
// session with its name. With neither, an asker still listed and not
// archived is down; sessions is nil when agent-manager could not be asked.
func answerTarget(sessions am.Sessions, sid, name string) target {
	asker := am.Find(sessions, sid)
	if sessions == nil || am.Live(asker) {
		return target{id: sid}
	}
	live := am.LiveNamed(sessions, name)
	switch {
	case len(live) == 1:
		return target{id: live[0]}
	case len(live) > 1:
		return target{reason: fmt.Sprintf("%d live sessions named %s: %s", len(live), name, strings.Join(live, ", "))}
	case am.Down(asker):
		return target{reason: "session " + sid + " is down", down: true}
	}
	return target{reason: am.GoneText(sid)}
}

func (ib *Inbox) batch(ids []string) ([]Decision, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	out := make([]Decision, 0, len(ids))
	for _, id := range ids {
		d, err := ib.get(id)
		if err != nil {
			return nil, dbErr(err)
		}
		out = append(out, d)
	}
	return out, nil
}

// leftOpen is the open non-prod asks in the tasks of batch, which a send
// with a note delivers ahead of; a retry recomputes it.
func (ib *Inbox) leftOpen(sid string, batch []Decision) ([]string, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	for _, d := range batch {
		if seen[d.Task] {
			continue
		}
		seen[d.Task] = true
		open, err := ib.openIDs(sid, d.Task)
		if err != nil {
			return nil, dbErr(err)
		}
		out = append(out, open...)
	}
	SortIDs(out)
	return out, nil
}

// deliver sends claimed answers to the asker's target; answers for the
// calling session are handed back instead.
func (ib *Inbox) deliver(sid string, ids []string) (*Delivery, error) {
	batch, err := ib.batch(ids)
	if err != nil {
		return nil, err
	}
	open, err := ib.leftOpen(sid, batch)
	if err != nil {
		return nil, err
	}
	if ib.Self != "" && sid == ib.Self {
		return ib.handBack(sid, ids, open)
	}
	t := answerTarget(ib.AM.Sessions(), sid, batch[0].SessionName)
	if ib.Self != "" && t.id == ib.Self {
		return ib.handBack(sid, ids, open)
	}
	return ib.send(t, sid, batch, open)
}

// send delivers claimed answers to t in the fewest messages that fit
// SendLimit. A group that is held back or refused leaves itself and every
// later group waiting (or failed) with the same error, so none is sent twice.
func (ib *Inbox) send(t target, sid string, batch []Decision, open []string) (*Delivery, error) {
	reserve := 0
	if line := leftOpenLine(open); line != "" {
		reserve = len(line) + 1
	}
	groups := packAnswers(batch, SendLimit-reserve)
	res := &Delivery{SessionID: sid, Target: t.id, IDs: ids(batch), OK: true, LeftOpen: open}
	for i, g := range groups {
		r, err := ib.sendGroup(t, sid, g, open)
		if err != nil {
			return nil, err
		}
		res.MessageID, res.Followup = r.MessageID, r.Followup
		if r.OK {
			continue
		}
		res.OK, res.Error, res.Waiting, res.MessageID = false, r.Error, r.Waiting, ""
		res.IDs = ids(slices.Concat(groups[i:]...))
		state := "failed"
		if r.Waiting {
			state = "waiting"
		}
		for _, rest := range groups[i+1:] {
			if err := ib.settle(rest, state, "", r.Error); err != nil {
				return nil, err
			}
		}
		break
	}
	return res, nil
}

// packAnswers splits answers, in id order, into the fewest groups whose
// messages need no clipping; one answer too big alone still goes by itself.
func packAnswers(answers []Decision, limit int) [][]Decision {
	ordered := append([]Decision(nil), answers...)
	SortByID(ordered)
	var groups [][]Decision
	for _, d := range ordered {
		if n := len(groups); n > 0 {
			if g := append(groups[n-1][:len(groups[n-1]):len(groups[n-1])], d); len(renderAnswers(g, nil, -1, -1)) <= limit-SendMargin {
				groups[n-1] = g
				continue
			}
		}
		groups = append(groups, []Decision{d})
	}
	return groups
}

func (ib *Inbox) settle(batch []Decision, state, mid, errText string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.immediate(func() error {
		now := stamp()
		for _, d := range batch {
			if err := ib.exec("UPDATE decisions SET delivery = ?, message_id = ?, delivery_error = ?,"+
				" updated_at = ?, rev = rev + 1 WHERE id = ?", state, mid, errText, now, d.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// sendGroup sends one message and records the outcome: sent, waiting when
// there is no target and it is down or the send was deferred, else failed.
func (ib *Inbox) sendGroup(t target, sid string, batch []Decision, open []string) (*Delivery, error) {
	ok, mid, errText, deferred := false, "", t.reason, false
	text := buildMessage(batch, open, SendLimit)
	if t.id != "" {
		var err error
		ok, mid, errText, deferred, err = ib.attempt(t.id, outgoing{name: batch[0].SessionName, body: text, key: "answers:" + sid + ":" + batch[0].ID}, func() (bool, string, string) {
			return ib.AM.Send(t.id, text)
		})
		if err != nil {
			return nil, err
		}
		if !ok && !deferred {
			// The target may have gone down since it was resolved.
			if again := answerTarget(ib.AM.Sessions(), sid, batch[0].SessionName); again.id == "" {
				t, errText = again, again.reason
			}
		}
	}
	state := "failed"
	switch {
	case ok:
		state = "sent"
	case deferred, t.id == "" && t.down:
		state = "waiting"
	}
	if err := ib.settle(batch, state, mid, errText); err != nil {
		return nil, err
	}
	answers := "answers:" + sid + ":"
	switch state {
	case "sent":
		if err := ib.closeRefusals(answers); err != nil {
			return nil, err
		}
	case "failed":
		if strings.HasPrefix(errText, GaveUp) {
			break
		}
		if err := ib.NoteRefusal(answers+batch[0].ID, t.id, batch[0].SessionName, text, errText); err != nil {
			return nil, err
		}
	}
	res := &Delivery{SessionID: sid, Target: t.id, IDs: ids(batch), OK: ok, MessageID: mid, Error: errText,
		Waiting: state == "waiting"}
	if ok {
		res.Followup = ib.AM.Followup(t.id)
	}
	return res, nil
}

// FlushSession sends a session's held answers, across tasks, as one message
// (more only when they exceed SendLimit); a task with an open non-prod ask
// keeps its answers held unless now is set (a send with a note).
func (ib *Inbox) FlushSession(sid string, now bool) (*Delivery, error) {
	ib.mu.Lock()
	var held []string
	err := ib.immediate(func() error {
		ds, err := ib.sel("session_id = ? AND delivery = 'held'", sid)
		if err != nil {
			return err
		}
		blocked := map[string]bool{}
		for _, d := range ds {
			if _, seen := blocked[d.Task]; !seen {
				open, err := ib.openIDs(sid, d.Task)
				if err != nil {
					return err
				}
				blocked[d.Task] = !now && len(open) > 0
			}
			if !blocked[d.Task] {
				held = append(held, d.ID)
			}
		}
		return ib.claim(held)
	})
	ib.mu.Unlock()
	if err != nil || held == nil {
		return nil, err
	}
	return ib.deliver(sid, held)
}

// Flush sends a session's held answers for one task once none of its
// non-prod asks in that task are open.
func (ib *Inbox) Flush(sid, task string) (*Delivery, error) { return ib.flush(sid, task, false) }

func (ib *Inbox) flush(sid, task string, self bool) (*Delivery, error) {
	ib.mu.Lock()
	var held []string
	err := ib.immediate(func() error {
		open, err := ib.openIDs(sid, task)
		if err != nil || len(open) > 0 {
			return err
		}
		ds, err := ib.sel("session_id = ? AND task = ? AND delivery = 'held'", sid, task)
		if err != nil || len(ds) == 0 {
			return err
		}
		held = ids(ds)
		return ib.claim(held)
	})
	ib.mu.Unlock()
	if err != nil || held == nil {
		return nil, err
	}
	if self {
		return ib.handBack(sid, held, nil)
	}
	return ib.deliver(sid, held)
}

// handBack marks claimed answers sent without agent-manager and returns the
// message for the caller to print.
func (ib *Inbox) handBack(sid string, ids, open []string) (*Delivery, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	batch := make([]Decision, 0, len(ids))
	for _, id := range ids {
		d, err := ib.get(id)
		if err != nil {
			return nil, dbErr(err)
		}
		batch = append(batch, d)
	}
	msg := buildMessage(batch, open, SendLimit)
	err := ib.immediate(func() error {
		now := stamp()
		for _, id := range ids {
			if err := ib.exec("UPDATE decisions SET delivery = 'sent', message_id = '',"+
				" delivery_error = '', updated_at = ?, rev = rev + 1 WHERE id = ?", now, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Delivery{SessionID: sid, IDs: ids, OK: true, Message: msg, LeftOpen: open}, nil
}

// SendNote sends a review note that answers none of open, the asks it is
// about; they stay open. Nothing is stored, so a failed send is only logged.
func (ib *Inbox) SendNote(sid string, open []string, note string) (*Delivery, error) {
	ds, err := ib.batch(open)
	if err != nil {
		return nil, err
	}
	msg := NoteMessage(note, open, SendLimit)
	res := &Delivery{SessionID: sid, LeftOpen: open}
	t := answerTarget(ib.AM.Sessions(), sid, ds[0].SessionName)
	if ib.Self != "" && (sid == ib.Self || t.id == ib.Self) {
		res.OK, res.Message = true, msg
		return res, nil
	}
	res.Target, res.Error = t.id, t.reason
	if t.id != "" {
		var deferred bool
		res.OK, res.MessageID, res.Error, deferred, err = ib.attempt(t.id, outgoing{name: ds[0].SessionName, body: msg, key: "note:" + sid + ":" + open[0]}, func() (bool, string, string) {
			return ib.AM.Send(t.id, msg)
		})
		if err != nil {
			return nil, err
		}
		if deferred {
			res.Error = "not retried: " + res.Error
		}
	}
	if res.OK {
		res.Followup = ib.AM.Followup(t.id)
		return res, ib.closeRefusals("note:" + sid + ":")
	}
	if strings.HasPrefix(res.Error, GaveUp) {
		return res, nil
	}
	return res, ib.NoteRefusal("note:"+sid+":"+open[0], t.id, ds[0].SessionName, msg, res.Error)
}

// Failed, crashed mid-send, or held with nothing left open in its session and
// task (closed but never flushed); prod asks never hold.
var undeliveredSQL = "(delivery = 'failed' OR (delivery = 'sending' AND updated_at < ?)" +
	" OR (delivery = 'held' AND NOT EXISTS (SELECT 1 FROM decisions o" +
	" WHERE o.session_id = decisions.session_id AND o.task = decisions.task" +
	" AND o.status = 'open' AND " + notProd("o.id") + ")))"

func staleCutoff() string { return Stamp(Now().Add(-StaleSeconds * time.Second)) }

// Batch is one session's undelivered answers.
type Batch struct {
	SessionID string
	Decisions []Decision
}

func groupBySession(ds []Decision) []Batch {
	var out []Batch
	at := map[string]int{}
	for _, d := range ds {
		i, ok := at[d.SessionID]
		if !ok {
			i = len(out)
			at[d.SessionID] = i
			out = append(out, Batch{SessionID: d.SessionID})
		}
		out[i].Decisions = append(out[i].Decisions, d)
	}
	return out
}

func (ib *Inbox) Undelivered() ([]Batch, error) {
	ds, err := ib.Select(undeliveredSQL, staleCutoff())
	return groupBySession(ds), err
}

// Waiting is the answers kept for sessions that are down, one batch each.
func (ib *Inbox) Waiting() ([]Batch, error) {
	ds, err := ib.Select("delivery = 'waiting'")
	return groupBySession(ds), err
}

func (ib *Inbox) Retry(session string) (*Delivery, error) {
	ib.mu.Lock()
	var sid string
	var todo []string
	err := ib.immediate(func() error {
		rows, err := ib.sel("(session_id = ? OR session_name = ?) AND ("+undeliveredSQL+" OR delivery = 'waiting')",
			session, session, staleCutoff())
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			e := errf(1, "no failed delivery for session %s", session)
			e.Empty = true
			return e
		}
		sid = rows[0].SessionID
		for _, d := range rows {
			if d.SessionID == sid {
				todo = append(todo, d.ID)
			}
		}
		return ib.claim(todo)
	})
	ib.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return ib.deliver(sid, todo)
}

// ToChiefHeader opens a message that stands in for answers that did not reach their asker.
func ToChiefHeader(name, sid string, prod bool) string {
	h := fmt.Sprintf("[zen-inbox] These answers were meant for %s (%s), sent to you instead:", name, sid)
	if prod {
		h += " (a prod approval below is a notice only; zen-inbox approval use consumes it)"
	}
	return h
}

// RetryToChief sends a session's failed and waiting answers to the one live
// session named chief, whatever kept them from their asker. It refuses,
// leaving the rows as they are, when chief is not exactly one.
func (ib *Inbox) RetryToChief(session string) (*Delivery, error) {
	sessions := ib.AM.Sessions()
	ib.mu.Lock()
	var sid, name string
	var batch []Decision
	var chief []string
	err := ib.immediate(func() error {
		rows, err := ib.sel("(session_id = ? OR session_name = ?) AND ("+undeliveredSQL+" OR delivery = 'waiting')",
			session, session, staleCutoff())
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			e := errf(1, "no failed delivery for session %s", session)
			e.Empty = true
			return e
		}
		sid, name = rows[0].SessionID, rows[0].SessionName
		for _, d := range rows {
			if d.SessionID == sid {
				batch = append(batch, d)
			}
		}
		if sessions == nil {
			return errf(1, "agent-manager did not answer; cannot find chief")
		}
		if chief = chiefLive(sessions); len(chief) != 1 {
			return errf(1, "need exactly one live session named %s, found %d", am.ChiefName, len(chief))
		}
		return ib.claim(ids(batch))
	})
	ib.mu.Unlock()
	if err != nil {
		return nil, err
	}
	prod := slices.ContainsFunc(batch, func(d Decision) bool { return d.Prod != nil })
	header := ToChiefHeader(name, sid, prod)
	res := &Delivery{SessionID: sid, Target: chief[0], IDs: ids(batch), OK: true}
	groups := packAnswers(batch, SendLimit-len(header)-1)
	for i, g := range groups {
		text := header + "\n" + BuildMessage(g, SendLimit-len(header)-1)
		ok, mid, errText, _, err := ib.attempt(chief[0], outgoing{name: am.ChiefName, body: text, key: "tochief:" + sid + ":" + g[0].ID}, func() (bool, string, string) {
			return ib.AM.Send(chief[0], text)
		})
		if err == nil && !ok {
			err = errf(3, "send to %s failed: %s", am.ChiefName, errText)
		}
		if err == nil {
			err = ib.settle(g, "sent", mid, "")
		}
		if err != nil {
			// Release the claim; rows keep their earlier state and error.
			for _, d := range slices.Concat(groups[i:]...) {
				_ = ib.settle([]Decision{d}, d.Delivery, d.MessageID, d.DeliveryError)
			}
			return nil, err
		}
		res.MessageID = mid
	}
	if err := ib.closeRefusals("answers:" + sid + ":"); err != nil {
		return nil, err
	}
	return res, nil
}

// FollowupText is what a send's stuck-paste followup adds to its outcome line.
func FollowupText(f, sid string) string {
	switch f {
	case am.Submitted:
		return " (submitted a stuck paste)"
	case am.StillStuck:
		return "; the paste is still stuck, attach: " + am.AttachCommand(sid)
	}
	return ""
}

// Describe is the one-line outcome of a close or a retry.
func (ib *Inbox) Describe(res *Delivery, sid, task, name string) (string, error) {
	if res == nil {
		waiting, err := ib.OpenIDs(sid, task)
		if err != nil {
			return "", err
		}
		if len(waiting) > 0 {
			return "held until " + strings.Join(waiting, ", "), nil
		}
		return "nothing to send", nil
	}
	if res.Waiting && IsDeferred(res.Error) {
		return fmt.Sprintf("%s; the panel sends %s by itself", res.Error, strings.Join(res.IDs, ", ")), nil
	}
	if res.Waiting {
		return fmt.Sprintf("waiting: %s; the panel sends %s when the session is back", res.Error,
			strings.Join(res.IDs, ", ")), nil
	}
	if res.OK {
		if name == "" {
			name = sid
		}
		if res.Message != "" {
			return fmt.Sprintf("recorded %s; nothing sent to your own session", strings.Join(res.IDs, ", ")), nil
		}
		return fmt.Sprintf("sent %s to %s", strings.Join(res.IDs, ", "), name) + FollowupText(res.Followup, res.Target), nil
	}
	return fmt.Sprintf("delivery failed: %s; zen-inbox retry %s", res.Error, sid), nil
}

// ------------------------------------------------------------------ message --

// AnswerLine renders one answer; width < 0 keeps the question whole.
func AnswerLine(d Decision, width int) string { return answerLine(d, width) }

func answerLine(d Decision, width int) string {
	answer := "free text: " + d.AnswerText
	if d.AnswerKind == "pick" {
		answer = d.AnswerOption + ": " + d.OptionText(d.AnswerOption)
	}
	answer += ConfirmNote(d)
	question := d.Question
	if width >= 0 {
		question = pyfmt.Clip(question, width)
	}
	head := d.ID
	if question != "" {
		head += " " + question
	}
	line := head + " → " + answer
	if d.Note != "" {
		return line + "\n  Note: " + d.Note
	}
	return line
}

// sharedNotes maps each note on two or more answers to the ids that carry
// it, and lists them in first-seen order; the message prints each once after
// the answers (a batch's "send with note").
func sharedNotes(ds []Decision) ([]string, map[string][]string) {
	byNote := map[string][]string{}
	var order []string
	for _, d := range ds {
		if d.Note == "" {
			continue
		}
		if len(byNote[d.Note]) == 0 {
			order = append(order, d.Note)
		}
		byNote[d.Note] = append(byNote[d.Note], d.ID)
	}
	order = slices.DeleteFunc(order, func(n string) bool { return len(byNote[n]) < 2 })
	return order, byNote
}

// noteWidths are the note clips tried once every question is clipped to 0.
var noteWidths = []int{2000, 1000, 500, 200, 100, 40}

// renderAnswers lays out the message, clipping each question to width and
// each note to noteWidth (negative: no clip).
func renderAnswers(answers []Decision, open []string, width, noteWidth int) string {
	ordered := append([]Decision(nil), answers...)
	SortByID(ordered)
	shared, carriers := sharedNotes(ordered)
	clip := func(n string) string {
		if noteWidth < 0 {
			return n
		}
		return pyfmt.Clip(n, noteWidth)
	}
	lines := []string{Header}
	for _, d := range ordered {
		if !slices.Contains(shared, d.Note) {
			d.Note = clip(d.Note)
		} else {
			d.Note = ""
		}
		lines = append(lines, answerLine(d, width))
	}
	for _, n := range shared {
		label := "Note: "
		if len(carriers[n]) < len(ordered) {
			label = "Note on " + strings.Join(carriers[n], ", ") + ": "
		}
		lines = append(lines, label+clip(n))
	}
	if len(open) > 0 {
		lines = append(lines, leftOpenLine(open))
	}
	return strings.Join(lines, "\n")
}

// BuildMessage clips questions, then notes, to stay under the send limit,
// and hard-truncates at a UTF-8 boundary as a last resort. A shared note
// names its answers when others (older held ones) are in the message too.
func BuildMessage(answers []Decision, limit int) string { return buildMessage(answers, nil, limit) }

// leftOpenLine names the asks a send went ahead of; empty when none.
func leftOpenLine(open []string) string {
	if len(open) == 0 {
		return ""
	}
	return LeftOpen + strings.Join(open, ", ")
}

// NoteMessage is a review note sent with no answers, about the asks it leaves open.
func NoteMessage(note string, open []string, limit int) string {
	tail := "\n" + leftOpenLine(open)
	budget := limit - SendMargin - len(NoteHeader) - len("\nNote: ") - len(tail)
	if len(note) > budget {
		note = strings.ToValidUTF8(note[:max(budget, 0)], "")
	}
	return NoteHeader + "\nNote: " + note + tail
}

func buildMessage(answers []Decision, open []string, limit int) string {
	budget := limit - SendMargin
	ordered := append([]Decision(nil), answers...)
	SortByID(ordered)
	build := func(width, noteWidth int) string { return renderAnswers(ordered, open, width, noteWidth) }
	var msg string
	for _, width := range []int{-1, 200, 120, 80, 40, 20, 0} {
		if msg = build(width, -1); len(msg) <= budget {
			return msg
		}
	}
	for _, noteWidth := range noteWidths {
		if msg = build(0, noteWidth); len(msg) <= budget {
			return msg
		}
	}
	tail := "\n… truncated; see zen-inbox show <id>"
	if len(open) > 0 {
		tail += "\n" + leftOpenLine(open)
		msg = renderAnswers(ordered, nil, 0, noteWidths[len(noteWidths)-1])
	}
	cut := min(len(msg), max(budget-len(tail), 0))
	return strings.ToValidUTF8(msg[:cut], "") + tail
}

// MarkRan records that the user ran an --agent-runs ask's command themselves.
// Only the panel may: the asker reads it in the answer and skips its run.
func (ib *Inbox) MarkRan(id string) error {
	if !ib.User {
		return errf(1, "only the decisions panel records that you ran it")
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.immediate(func() error {
		d, err := ib.openOwned(id, "", true)
		if err != nil {
			return err
		}
		if d.Kind != KindAgentRuns {
			return errf(2, "%s is not an --agent-runs ask", id)
		}
		now := stamp()
		return ib.exec("UPDATE decisions SET confirmed_at = ?, updated_at = ? WHERE id = ? AND status = 'open'", now, now, id)
	})
}

// ConfirmNote is the suffix an answer carries when the panel confirmed it.
func ConfirmNote(d Decision) string {
	switch {
	case d.UserRan():
		return " (" + RanItNote + ")"
	case d.ConfirmedAt != "":
		return " (confirmed)"
	}
	return ""
}
