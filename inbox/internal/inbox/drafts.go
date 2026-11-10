package inbox

// Draft is an answer staged in the panel but not sent: a pick, free text or
// a skip. Rev is the decision's rev when it was staged.
type Draft struct {
	ID, Kind, Option, Text string
	Rev                    int64
}

// schemaV4 keeps the panel's staged answers across a restart. No existing
// table changes, so a v3 reader still works.
const schemaV4 = `
CREATE TABLE decision_drafts (
    decision_id TEXT PRIMARY KEY REFERENCES decisions(id),
    kind TEXT NOT NULL,
    option TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    rev INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);
`

func (ib *Inbox) deleteDraft(id string) error {
	return ib.exec("DELETE FROM decision_drafts WHERE decision_id = ?", id)
}

// SaveDraft stores d for an open decision; a closed one is left without a
// draft. An Approve on a prod-action ask is never stored, so after a restart
// it is asked again with its confirm.
func (ib *Inbox) SaveDraft(d Draft) error {
	switch d.Kind {
	case "pick", "text", "skip":
	default:
		return errf(2, "bad draft kind %q", d.Kind)
	}
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return ib.immediate(func() error {
		dec, err := ib.get(d.ID)
		if err != nil {
			return err
		}
		if dec.Status != "open" || (dec.Prod != nil && d.Kind == "pick" && dec.OptionText(d.Option) == "Approve") {
			return ib.deleteDraft(d.ID)
		}
		return ib.exec("INSERT INTO decision_drafts (decision_id, kind, option, text, rev, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (decision_id) DO UPDATE SET kind = excluded.kind,"+
			" option = excluded.option, text = excluded.text, rev = excluded.rev,"+
			" updated_at = excluded.updated_at",
			d.ID, d.Kind, d.Option, d.Text, d.Rev, stamp())
	})
}

func (ib *Inbox) DeleteDraft(id string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return dbErr(ib.deleteDraft(id))
}

// ClearDrafts drops every draft of a session's decisions.
func (ib *Inbox) ClearDrafts(sid string) error {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return dbErr(ib.exec("DELETE FROM decision_drafts WHERE decision_id IN"+
		" (SELECT id FROM decisions WHERE session_id = ?)", sid))
}

// Drafts is every draft whose decision is still open at the rev it was
// staged against.
func (ib *Inbox) Drafts() (map[string]Draft, error) {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	rows, err := ib.conn.QueryContext(ctx, "SELECT r.decision_id, r.kind, r.option, r.text, r.rev"+
		" FROM decision_drafts r JOIN decisions d ON d.id = r.decision_id"+
		" WHERE d.status = 'open' AND d.rev = r.rev")
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	out := map[string]Draft{}
	for rows.Next() {
		var d Draft
		if err := rows.Scan(&d.ID, &d.Kind, &d.Option, &d.Text, &d.Rev); err != nil {
			return nil, dbErr(err)
		}
		out[d.ID] = d
	}
	return out, dbErr(rows.Err())
}
