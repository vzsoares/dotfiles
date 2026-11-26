package inbox

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/pyfmt"
)

func TestV10DBMigratesToCurrentKeepingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 10; v++ {
		for _, stmt := range strings.Split(Migrations[v], ";") {
			if strings.TrimSpace(stmt) != "" {
				if _, err := raw.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, stmt := range []string{
		"PRAGMA user_version = 10",
		"INSERT INTO decisions (id, task, seq, session_id, session_name, question, context, created_at, updated_at)" +
			" VALUES ('7.1', '7', 1, 's1', 'spec-s1', 'Old?', 'ctx', '2026-01-01T00:00:00+00:00', '2026-01-01T00:00:00+00:00')",
		"INSERT INTO decisions (id, task, seq, session_id, session_name, question, status, answer_kind, answer_text," +
			" delivery, rev, created_at, updated_at) VALUES ('7.2', '7', 2, 's1', 'spec-s1', 'Done?', 'answered'," +
			" 'text', 'yes', 'sent', 3, '2026-01-01T00:00:00+00:00', '2026-01-02T00:00:00+00:00')",
		"INSERT INTO decision_options VALUES ('7.1', 'a', 'x'), ('7.1', 'b', 'y')",
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	create := createSQL(t, raw, "decisions")
	before := tableRows(t, raw, "decisions", v10Columns)
	_ = raw.Close()

	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := ib.userVersion(); v != SchemaVersion {
		t.Fatalf("user_version %d", v)
	}
	if got := createSQL(t, ib.conn, "decisions"); got != withAdded(create) {
		t.Fatalf("decisions:\n%s", got)
	}
	for _, id := range []string{"7.1", "7.2"} {
		if d := get(t, ib, id); d.Body != "" {
			t.Fatalf("%s body %q", id, d.Body)
		}
	}
	if d := get(t, ib, "7.2"); d.Rev != 3 || d.AnswerText != "yes" || d.Delivery != "sent" {
		t.Fatalf("%+v", d)
	}
	_ = ib.Close()

	// A v10 binary sees a later user_version as current: it reads and writes its own columns.
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if got := tableRows(t, raw, "decisions", v10Columns); got != before {
		t.Fatalf("rows changed:\n%s\n%s", got, before)
	}
	if _, err := raw.Exec("INSERT INTO decisions (id, task, seq, session_id, session_name, question, created_at, updated_at)" +
		" VALUES ('7.3', '7', 3, 's1', 'spec-s1', 'From v10?', 'x', 'x')"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	ib, err = Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer ib.Close()
	if d := get(t, ib, "7.3"); d.Body != "" || d.Question != "From v10?" {
		t.Fatalf("%+v", d)
	}
	d, err := ib.Ask(AskInput{Task: "7", Question: "New?", Body: "- one\n- two", Options: []string{"a", "b"}, AgentRuns: true,
		SessionID: "s1", Name: "spec-s1"})
	if err != nil || d.ID != "7.4" || d.Body != "- one\n- two" {
		t.Fatalf("%+v %v", d, err)
	}
}

func jsonKeys(o pyfmt.Object) string {
	keys := make([]string, len(o))
	for i, kv := range o {
		keys[i] = kv.Key
	}
	return strings.Join(keys, ",")
}

func TestAskBodyStoredAndInJSONOnlyWhenSet(t *testing.T) {
	ib, _, _ := open(t)
	plain := get(t, ib, ask(t, ib, "7", "s1", false, "Plain?"))
	if strings.Contains(jsonKeys(plain.JSON()), "body") {
		t.Fatalf("body key on an ask without one: %s", jsonKeys(plain.JSON()))
	}
	d, err := ib.Ask(AskInput{Task: "7", Question: "Run?", Body: "```sh\nmake x\n```", Options: []string{"a", "b"}, AgentRuns: true,
		SessionID: "s1", Name: "spec-s1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, ib, d.ID); got.Body != "```sh\nmake x\n```" || !strings.HasSuffix(jsonKeys(got.JSON()), ",brief,body,kind,confirmed_at") {
		t.Fatalf("%+v %s", got, jsonKeys(got.JSON()))
	}
	var ie *Error
	_, err = ib.Ask(AskInput{Task: "7", Question: "Big?", Body: strings.Repeat("x", BodyMax+1), SessionID: "s1"})
	if !errors.As(err, &ie) || ie.Code != 2 {
		t.Fatalf("%v", err)
	}
	if _, err := ib.Ask(AskInput{Task: "7", Question: "Fits?", Body: strings.Repeat("x", BodyMax), SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
}

func TestAmendBody(t *testing.T) {
	ib, _, _ := open(t)
	d, err := ib.Ask(AskInput{Task: "7", Question: "Run?", Body: "old", Context: "ctx", SessionID: "s1", Name: "spec-s1"})
	if err != nil {
		t.Fatal(err)
	}
	body := "new"
	got, err := ib.Amend(d.ID, "s1", false, AmendInput{Body: &body})
	if err != nil || got.Body != "new" || got.Rev != d.Rev+1 || got.Question != "Run?" || got.Context != "ctx" {
		t.Fatalf("%+v %v", got, err)
	}
	q := "Really?"
	if got, err = ib.Amend(d.ID, "s1", false, AmendInput{Question: &q}); err != nil || got.Body != "new" {
		t.Fatalf("amending the question dropped the body: %+v %v", got, err)
	}
	empty := ""
	if got, err = ib.Amend(d.ID, "s1", false, AmendInput{Body: &empty}); err != nil || got.Body != "" {
		t.Fatalf("%+v %v", got, err)
	}
	big := strings.Repeat("x", BodyMax+1)
	var ie *Error
	if _, err = ib.Amend(d.ID, "s1", false, AmendInput{Body: &big}); !errors.As(err, &ie) || ie.Code != 2 {
		t.Fatalf("%v", err)
	}
}

func TestBodyStaysOutOfTheDeliveredMessage(t *testing.T) {
	ib, fake, _ := open(t)
	d, err := ib.Ask(AskInput{Task: "7", Question: "Run?", Body: "SECRET-BODY", Options: []string{"yes", "no"},
		SessionID: "s1", Name: "spec-s1"})
	if err != nil {
		t.Fatal(err)
	}
	letter := "a"
	if _, err := ib.Answer(d.ID, &letter, nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if len(fake.sends) != 1 || strings.Contains(fake.sends[0][1], "SECRET-BODY") || !strings.Contains(fake.sends[0][1], "7.1 Run? → a: yes") {
		t.Fatalf("%q", fake.sends)
	}
}
