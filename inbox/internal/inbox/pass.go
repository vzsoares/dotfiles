package inbox

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
)

// Outcome is one line of a pass: what was sent to a target, left waiting for
// it, or failed.
type Outcome struct {
	// Kind is "answers", "reply" or "chat"; Name is the target's name.
	Kind, Name string
	// Count is how many answers, replies or chats the line covers.
	Count        int
	Sent, Failed bool
	Text         string
}

// passRun is one pass's view of the world: the sessions listed once, and the
// targets that already got their one message.
type passRun struct {
	sessions am.Sessions
	sent     map[string]bool
}

// holdBack is why t gets nothing this pass, "" when it may be sent to.
func (ib *Inbox) holdBack(p *passRun, t target) string {
	switch {
	case p.sessions == nil:
		return "agent-manager could not list sessions"
	case t.id == "" && t.down:
		return t.reason
	case t.id != "" && t.id == ib.Self:
		return "the target is your own session"
	case t.id != "" && p.sent[t.id]:
		return "one message per session per pass"
	case t.id != "":
		why, err := ib.SendHold(t.id)
		if err != nil {
			return err.Error()
		}
		return why
	}
	return ""
}

// Pass sends what waits for a session that was down and is live again:
// answers as one message per session, then note replies oldest first, then
// the oldest chat; at most one message per target. Every item is claimed in
// its own transaction first, so concurrent passes send it once. With nothing
// waiting it runs no external command; otherwise it lists sessions once and
// makes no send attempt to a target that is still down.
func (ib *Inbox) Pass() ([]Outcome, error) {
	answers, err := ib.Waiting()
	if err != nil {
		return nil, err
	}
	notes, err := ib.WaitingNotes()
	if err != nil {
		return nil, err
	}
	ib.mu.Lock()
	chats, err := ib.selChats(chatClaimable, staleCutoff())
	ib.mu.Unlock()
	if err != nil {
		return nil, dbErr(err)
	}
	if len(answers)+len(notes)+len(chats) == 0 {
		return nil, nil
	}
	p := &passRun{sessions: ib.AM.Sessions(), sent: map[string]bool{}}
	var out []Outcome
	add := func(o *Outcome, err error) error {
		if o != nil {
			out = append(out, *o)
		}
		return err
	}
	for _, b := range answers {
		if err := add(ib.passAnswers(p, b)); err != nil {
			return out, err
		}
	}
	for _, n := range notes {
		if err := add(ib.passReply(p, n)); err != nil {
			return out, err
		}
	}
	if len(chats) > 0 {
		if err := add(ib.passChat(p, chats)); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (ib *Inbox) passAnswers(p *passRun, b Batch) (*Outcome, error) {
	o := &Outcome{Kind: "answers", Name: b.Decisions[0].SessionName, Count: len(b.Decisions)}
	t := answerTarget(p.sessions, b.SessionID, o.Name)
	if why := ib.holdBack(p, t); why != "" {
		o.Text = "waiting " + strings.Join(ids(b.Decisions), ", ") + " for " + o.Name + ": " + why
		return o, nil
	}
	ib.mu.Lock()
	var batch []Decision
	err := ib.immediate(func() error {
		var err error
		if batch, err = ib.sel("session_id = ? AND delivery = 'waiting'", b.SessionID); err != nil {
			return err
		}
		return ib.claim(ids(batch))
	})
	ib.mu.Unlock()
	if err != nil || len(batch) == 0 {
		return nil, err
	}
	p.sent[t.id] = true
	res, err := ib.send(t, b.SessionID, batch)
	if err != nil {
		return nil, err
	}
	o.Count, o.Sent, o.Failed = len(batch), res.OK, !res.OK && !res.Waiting
	if o.Text, err = ib.Describe(res, b.SessionID, "", o.Name); err != nil {
		return nil, err
	}
	return o, nil
}

func (ib *Inbox) passReply(p *passRun, n Note) (*Outcome, error) {
	o := &Outcome{Kind: "reply", Name: n.SessionName, Count: 1}
	t := replyTarget(p.sessions, n)
	if why := ib.holdBack(p, t); why != "" {
		o.Text = "waiting reply " + n.Label() + " for " + o.Name + ": " + why
		return o, nil
	}
	ns, err := ib.claimNotes("id = ? AND delivery = 'waiting'", n.ID)
	if err != nil || len(ns) == 0 {
		return nil, err
	}
	p.sent[t.id] = true
	res, err := ib.sendReplyTo(ns[0], t)
	if err != nil {
		return nil, err
	}
	o.Sent, o.Failed, o.Text = res.OK, !res.OK && !res.Waiting, res.Describe()
	return o, nil
}

// passChat sends the oldest claimable chat. zen-chief resolves chief itself;
// the pass only attempts it while a live session is named chief.
func (ib *Inbox) passChat(p *passRun, chats []Chat) (*Outcome, error) {
	o := &Outcome{Kind: "chat", Name: am.ChiefName, Count: len(chats)}
	live := chiefLive(p.sessions)
	t := target{reason: "no live session named " + am.ChiefName, down: true}
	if len(live) > 0 {
		t = target{id: live[0]}
	}
	if why := ib.holdBack(p, t); why != "" {
		o.Text = fmt.Sprintf("waiting %d message(s) for %s: %s", len(chats), o.Name, why)
		return o, nil
	}
	c, ok, err := ib.claimChat()
	if err != nil || !ok {
		return nil, err
	}
	p.sent[t.id] = true
	if c, err = ib.sendChat(c); err != nil {
		return nil, err
	}
	label := "message c" + strconv.FormatInt(c.ID, 10)
	o.Count = 1
	switch c.Status {
	case "waiting":
		o.Text = "waiting " + label + " for " + o.Name + ": " + c.Error
	case "failed":
		o.Failed, o.Text = true, label+" to "+o.Name+" failed: "+c.Error
	default:
		o.Sent, o.Text = true, label+" sent to "+o.Name
	}
	return o, nil
}
