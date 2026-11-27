package inbox

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

// Delete kinds and modes, as the CLI reports them.
const (
	KindDecision = "decision"
	KindNote     = "note"
	KindChat     = "chat"
	ModeHide     = "hide"
	ModePurge    = "purge"
)

var decisionIDRE = regexp.MustCompile(`^(.+)\.(\d+|g\d*)$`)

type delTarget struct {
	kind string
	id   string
	n    int64
}

// ParseTarget reads n12, c151, 580.1, 580.g or 580.g2; a bare number could
// be a task, so it is refused with the rest.
func ParseTarget(s string) (kind, id string, err error) {
	bad := errf(2, "bad id %s: use nN, cN, TASK.N or TASK.gN", pyfmt.Repr(s))
	if len(s) > 1 && (s[0] == 'n' || s[0] == 'c') && digits(s[1:]) {
		if s[0] == 'n' {
			return KindNote, s, nil
		}
		return KindChat, s, nil
	}
	m := decisionIDRE.FindStringSubmatch(s)
	if m == nil {
		return "", "", bad
	}
	suffix := m[2]
	if suffix[0] == 'g' && strings.TrimLeft(suffix[1:], "0") == "1" {
		suffix = "g"
	}
	return KindDecision, m[1] + "." + suffix, nil
}

// Deleted is one row a delete hid or purged (or would, in a dry run).
type Deleted struct{ ID, Kind, Mode string }

// Skipped is one row a guard kept. Reason follows "skipped ID: "; Msg is the
// whole refusal an explicit ID gets.
type Skipped struct{ ID, Reason, Msg string }

// DeleteResult is what Delete did. Refused means an explicit ID failed, so
// nothing was written and Deleted is empty.
type DeleteResult struct {
	Deleted []Deleted
	Skipped []Skipped
	Refused bool
}

type row struct {
	kind, id string
	hidden   bool
	// refuse is the guard that kept the row, if any.
	refuse *Skipped
	// purge and hide write the removal or the hidden stamp.
	purge func() error
	hide  func(at string) error
}

// Delete hides ids (and every closed decision, note and chat of task) or
// purges them. Checks and writes share one BEGIN IMMEDIATE, so a concurrent
// answer, flush, retry or pass lands before or sees the row gone.
func (ib *Inbox) Delete(ids []string, task string, purge, dryRun bool) (DeleteResult, error) {
	var targets []delTarget
	for _, s := range ids {
		kind, id, err := ParseTarget(s)
		if err != nil {
			return DeleteResult{}, err
		}
		t := delTarget{kind: kind, id: id}
		if kind != KindDecision {
			t.n, _ = strconv.ParseInt(id[1:], 10, 64)
		}
		targets = append(targets, t)
	}
	if task != "" && !digits(task) {
		return DeleteResult{}, errf(2, "bad --task %s: digits only", pyfmt.Repr(task))
	}
	if len(targets) == 0 && task == "" {
		return DeleteResult{}, errf(2, "give IDs or --task")
	}
	mode := ModeHide
	if purge {
		mode = ModePurge
	}
	var res DeleteResult
	ib.mu.Lock()
	defer ib.mu.Unlock()
	err := ib.immediate(func() error {
		res = DeleteResult{}
		var rows []row
		seen := map[string]bool{}
		add := func(r row, strict bool) {
			if seen[r.id] {
				return
			}
			seen[r.id] = true
			if r.refuse == nil && r.hidden && !purge && strict {
				r.refuse = &Skipped{ID: r.id, Reason: "already deleted", Msg: r.id + " is already deleted"}
			}
			if r.refuse != nil {
				res.Skipped = append(res.Skipped, *r.refuse)
				if strict {
					res.Refused = true
				}
				return
			}
			rows = append(rows, r)
		}
		for _, t := range targets {
			r, found, err := ib.deleteRow(t)
			if err != nil {
				return err
			}
			if !found {
				msg := "no " + t.kind + " " + t.id
				if !seen[t.id] {
					seen[t.id] = true
					res.Skipped = append(res.Skipped, Skipped{ID: t.id, Reason: "not found", Msg: msg})
					res.Refused = true
				}
				continue
			}
			add(r, true)
		}
		if task != "" {
			found, err := ib.taskRows(task, purge)
			if err != nil {
				return err
			}
			for _, r := range found {
				add(r, false)
			}
		}
		if res.Refused {
			return nil
		}
		at := stamp()
		for _, r := range rows {
			res.Deleted = append(res.Deleted, Deleted{ID: r.id, Kind: r.kind, Mode: mode})
			if dryRun {
				continue
			}
			var err error
			if purge {
				err = r.purge()
			} else {
				err = r.hide(at)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}

// taskRows is every decision, note and chat of task the way --task sees
// them: hidden rows only matter to a purge.
func (ib *Inbox) taskRows(task string, purge bool) ([]row, error) {
	var out []row
	ds, err := ib.selAny("task = ?", task)
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		if d.DeletedAt != "" && !purge {
			continue
		}
		out = append(out, ib.decisionRow(d))
	}
	ns, err := ib.selNotesAny("task = ?", task)
	if err != nil {
		return nil, err
	}
	for _, n := range ns {
		if n.DeletedAt != "" && !purge {
			continue
		}
		out = append(out, ib.noteRow(n))
	}
	cs, err := ib.selChatsAny("task = ?", task)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		if c.DeletedAt != "" && !purge {
			continue
		}
		out = append(out, ib.chatRow(c))
	}
	return out, nil
}

func (ib *Inbox) deleteRow(t delTarget) (row, bool, error) {
	switch t.kind {
	case KindDecision:
		ds, err := ib.selAny("id = ?", t.id)
		if err != nil || len(ds) == 0 {
			return row{}, false, err
		}
		return ib.decisionRow(ds[0]), true, nil
	case KindNote:
		ns, err := ib.selNotesAny("id = ?", t.n)
		if err != nil || len(ns) == 0 {
			return row{}, false, err
		}
		return ib.noteRow(ns[0]), true, nil
	}
	cs, err := ib.selChatsAny("id = ?", t.n)
	if err != nil || len(cs) == 0 {
		return row{}, false, err
	}
	return ib.chatRow(cs[0]), true, nil
}

func refusal(id, reason, msg string) *Skipped { return &Skipped{ID: id, Reason: reason, Msg: msg} }

func (ib *Inbox) decisionRow(d Decision) row {
	r := row{kind: KindDecision, id: d.ID, hidden: d.DeletedAt != ""}
	switch {
	case d.Status == "open":
		r.refuse = refusal(d.ID, "open; withdraw it first", d.ID+" is open; withdraw it first")
	case d.Prod != nil && approved(d):
		r.refuse = refusal(d.ID, "an approved prod ask; it stays as the audit trail",
			d.ID+" is an approved prod ask; it stays as the audit trail")
	case d.Delivery == "held" || d.Delivery == "sending" || d.Delivery == "waiting":
		r.refuse = refusal(d.ID, "has an answer waiting for delivery", d.ID+" has an answer waiting for delivery")
	}
	r.hide = func(at string) error {
		return ib.exec("UPDATE decisions SET deleted_at = ? WHERE id = ?", at, d.ID)
	}
	r.purge = func() error {
		gate := 0
		if d.Gate {
			gate = 1
		}
		if err := ib.exec("INSERT INTO decision_seq (task, gate, seq) VALUES (?, ?, ?)"+
			" ON CONFLICT (task, gate) DO UPDATE SET seq = MAX(seq, excluded.seq)",
			d.Task, gate, d.Seq); err != nil {
			return err
		}
		for _, table := range []string{"decision_options", "decision_drafts", "prod_actions"} {
			if err := ib.exec("DELETE FROM "+table+" WHERE decision_id = ?", d.ID); err != nil {
				return err
			}
		}
		return ib.exec("DELETE FROM decisions WHERE id = ?", d.ID)
	}
	return r
}

// approved is a prod ask whose answer picked Approve, used or not.
func approved(d Decision) bool {
	return d.AnswerKind == "pick" && d.OptionText(d.AnswerOption) == "Approve"
}

const inFlight = "in flight; try again in a minute"

func (ib *Inbox) noteRow(n Note) row {
	r := row{kind: KindNote, id: n.Label(), hidden: n.DeletedAt != ""}
	if n.Delivery == "sending" && n.ClaimedAt >= staleCutoff() {
		r.refuse = refusal(r.id, inFlight, r.id+" "+inFlight)
	}
	r.hide = func(at string) error {
		return ib.exec("UPDATE notes SET deleted_at = ? WHERE id = ?", at, n.ID)
	}
	r.purge = func() error {
		if err := ib.exec("DELETE FROM note_replies WHERE note_id = ?", n.ID); err != nil {
			return err
		}
		return ib.exec("DELETE FROM notes WHERE id = ?", n.ID)
	}
	return r
}

func (ib *Inbox) chatRow(c Chat) row {
	r := row{kind: KindChat, id: c.Label(), hidden: c.DeletedAt != ""}
	if c.Status == "sending" && c.ClaimedAt >= staleCutoff() {
		r.refuse = refusal(r.id, inFlight, r.id+" "+inFlight)
	}
	r.hide = func(at string) error {
		return ib.exec("UPDATE chats SET deleted_at = ? WHERE id = ?", at, c.ID)
	}
	r.purge = func() error { return ib.exec("DELETE FROM chats WHERE id = ?", c.ID) }
	return r
}
