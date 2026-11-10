package inbox

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

// ProdAction is what a prod-action ask approves. Release tools match on
// these fields, never on the question text.
type ProdAction struct {
	Action, Repo, Branch, Sha string
	UsedAt, UsedBy            string
}

// ProdOptions are a prod-action ask's options; only a pick of "Approve" counts.
var ProdOptions = []string{"Approve", "Hold"}

var (
	shaRE    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	actionRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// CheckProd validates the bound fields; every failure is a usage error.
func CheckProd(p ProdAction) error {
	if err := CheckTarget(p); err != nil {
		return err
	}
	if p.Sha == "" {
		return errf(2, "--sha is required")
	}
	if !shaRE.MatchString(p.Sha) {
		return errf(2, "--sha must be 40 lowercase hex characters, got %s", pyfmt.Repr(p.Sha))
	}
	return nil
}

// CheckTarget validates action, repo and branch.
func CheckTarget(p ProdAction) error {
	switch {
	case !actionRE.MatchString(p.Action):
		return errf(2, "bad prod action %s: lowercase letters, digits or -", pyfmt.Repr(p.Action))
	case pyfmt.Blank(p.Repo) || pyfmt.Blank(p.Branch):
		return errf(2, "--repo and --branch are required")
	}
	return nil
}

// ProdQuestion is the question a prod-action ask shows.
func ProdQuestion(p ProdAction) string {
	return fmt.Sprintf("%s %s %s@%s?", p.Action, p.Repo, p.Branch, p.Sha[:12])
}

// AskProd records a prod-action ask with its standard question and options.
func (ib *Inbox) AskProd(in AskInput, p ProdAction) (Decision, error) {
	if err := CheckProd(p); err != nil {
		return Decision{}, err
	}
	in.Question, in.Options, in.Rec, in.Gate, in.Prod = ProdQuestion(p), ProdOptions, "a", false, &p
	return ib.Ask(in)
}

func (ib *Inbox) insertProd(id string, p ProdAction) error {
	return ib.exec("INSERT INTO prod_actions (decision_id, action, repo, branch, sha) VALUES (?, ?, ?, ?, ?)",
		id, p.Action, p.Repo, p.Branch, p.Sha)
}

func (ib *Inbox) prodAction(id string) (ProdAction, bool, error) {
	var p ProdAction
	err := ib.conn.QueryRowContext(ctx, "SELECT action, repo, branch, sha, used_at, used_by"+
		" FROM prod_actions WHERE decision_id = ?", id).
		Scan(&p.Action, &p.Repo, &p.Branch, &p.Sha, &p.UsedAt, &p.UsedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	return p, err == nil, err
}

// approvalError explains why an approval does not cover the requested action.
func approvalError(d Decision, p, want ProdAction) string {
	if reason := approveError(d); reason != "" {
		return reason
	}
	if p.UsedAt != "" {
		return fmt.Sprintf("already used by %s at %s", p.UsedBy, p.UsedAt)
	}
	return mismatch(p, want)
}

func approveError(d Decision) string {
	switch {
	case d.Status == "open":
		return "not answered"
	case d.Status != "answered":
		return d.Status
	case d.AnswerKind == "talk":
		return "resolved in talk, not Approve"
	case d.AnswerKind != "pick":
		return "answered with free text, not Approve"
	case d.OptionText(d.AnswerOption) != "Approve":
		return "answered " + d.OptionText(d.AnswerOption)
	}
	return ""
}

// mismatch compares the bound fields; an empty want.Sha matches any sha.
func mismatch(p, want ProdAction) string {
	switch {
	case p.Action != want.Action:
		return fmt.Sprintf("action mismatch: approved %s, requested %s", p.Action, want.Action)
	case p.Repo != want.Repo:
		return fmt.Sprintf("repo mismatch: approved %s, releasing %s", p.Repo, want.Repo)
	case p.Branch != want.Branch:
		return fmt.Sprintf("branch mismatch: approved %s, releasing %s", p.Branch, want.Branch)
	case want.Sha != "" && p.Sha != want.Sha:
		return fmt.Sprintf("sha mismatch: approved %s, releasing %s", p.Sha, want.Sha)
	}
	return ""
}

// Approval checks that id approves want and, when by is set, marks it used in
// the same BEGIN IMMEDIATE transaction, so one approval is consumed once.
// A rejection is an *Error with code 1.
func (ib *Inbox) Approval(id string, want ProdAction, by string) (ProdAction, error) {
	if err := CheckProd(want); err != nil {
		return ProdAction{}, err
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var p ProdAction
	err := ib.immediate(func() error {
		d, err := ib.get(id)
		var ie *Error
		if errors.As(err, &ie) {
			return errf(1, "approval %s: unknown id", id)
		} else if err != nil {
			return err
		}
		if d.Prod == nil {
			return errf(1, "approval %s: not a prod-action ask", id)
		}
		p = *d.Prod
		if reason := approvalError(d, p, want); reason != "" {
			return errf(1, "approval %s: %s", id, reason)
		}
		if by == "" {
			return nil
		}
		return ib.consume(id, &p, by)
	})
	return p, err
}

func (ib *Inbox) consume(id string, p *ProdAction, by string) error {
	p.UsedAt, p.UsedBy = stamp(), by
	res, err := ib.conn.ExecContext(ctx, "UPDATE prod_actions SET used_at = ?, used_by = ?"+
		" WHERE decision_id = ? AND used_at = ''", p.UsedAt, p.UsedBy, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return errf(1, "approval %s: already used", id)
	}
	return nil
}

// ApprovalMatch finds the oldest approved, unused ask bound to exactly want
// and, when by is set, consumes it in the same BEGIN IMMEDIATE transaction.
func (ib *Inbox) ApprovalMatch(want ProdAction, by string) (string, ProdAction, error) {
	if err := CheckProd(want); err != nil {
		return "", ProdAction{}, err
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	var id string
	var p ProdAction
	err := ib.immediate(func() error {
		err := ib.conn.QueryRowContext(ctx, "SELECT d.id FROM decisions d"+
			" JOIN prod_actions a ON a.decision_id = d.id"+
			" JOIN decision_options o ON o.decision_id = d.id AND o.letter = d.answer_option"+
			" WHERE a.action = ? AND a.repo = ? AND a.branch = ? AND a.sha = ? AND a.used_at = ''"+
			" AND d.status = 'answered' AND d.answer_kind = 'pick' AND o.text = 'Approve'"+
			" ORDER BY d.created_at, d.rowid LIMIT 1",
			want.Action, want.Repo, want.Branch, want.Sha).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(1, "no approved unused ask for %s", strings.TrimSuffix(ProdQuestion(want), "?"))
		} else if err != nil {
			return err
		}
		p = want
		if by == "" {
			return nil
		}
		return ib.consume(id, &p, by)
	})
	return id, p, err
}

// ApprovalUsed returns the sha of a used, Approve-answered ask whose action,
// repo and branch match want. It never writes.
func (ib *Inbox) ApprovalUsed(id string, want ProdAction) (string, error) {
	if err := CheckTarget(want); err != nil {
		return "", err
	}
	want.Sha = ""
	ib.mu.Lock()
	defer ib.mu.Unlock()
	d, err := ib.get(id)
	var ie *Error
	if errors.As(err, &ie) {
		return "", errf(1, "approval %s: unknown id", id)
	} else if err != nil {
		return "", dbErr(err)
	}
	if d.Prod == nil {
		return "", errf(1, "approval %s: not a prod-action ask", id)
	}
	reason := approveError(d)
	if reason == "" && d.Prod.UsedAt == "" {
		reason = "not used"
	}
	if reason == "" {
		reason = mismatch(*d.Prod, want)
	}
	if reason != "" {
		return "", errf(1, "approval %s: %s", id, reason)
	}
	return d.Prod.Sha, nil
}
