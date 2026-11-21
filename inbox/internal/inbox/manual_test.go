package inbox

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func askManual(t *testing.T, ib *Inbox, in AskInput) Decision {
	t.Helper()
	in.Task, in.Manual, in.SessionID, in.Name = "929", true, "s1", "spec-s1"
	if in.Question == "" {
		in.Question = "Rotate the API key?"
	}
	d, err := ib.Ask(in)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestV11DBMigratesToV12KeepingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 11; v++ {
		for _, stmt := range strings.Split(Migrations[v], ";") {
			if strings.TrimSpace(stmt) != "" {
				if _, err := raw.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, stmt := range []string{
		"PRAGMA user_version = 11",
		"INSERT INTO decisions (id, task, seq, session_id, session_name, question, body, created_at, updated_at)" +
			" VALUES ('7.1', '7', 1, 's1', 'spec-s1', 'Old?', 'steps', '2026-01-01T00:00:00+00:00', '2026-01-01T00:00:00+00:00')",
		"INSERT INTO decisions (id, task, seq, session_id, session_name, question, status, answer_kind, answer_option," +
			" delivery, rev, created_at, updated_at) VALUES ('7.2', '7', 2, 's1', 'spec-s1', 'Done?', 'answered'," +
			" 'pick', 'a', 'sent', 3, '2026-01-01T00:00:00+00:00', '2026-01-02T00:00:00+00:00')",
		"INSERT INTO decision_options VALUES ('7.1', 'a', 'x'), ('7.1', 'b', 'y'), ('7.2', 'a', 'Done'), ('7.2', 'b', 'Later')",
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	v11Columns := strings.TrimSuffix(columns, ", kind, confirmed_at")
	create := createSQL(t, raw, "decisions")
	before := tableRows(t, raw, "decisions", v11Columns)
	_ = raw.Close()

	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := ib.userVersion(); v != SchemaVersion {
		t.Fatalf("user_version %d", v)
	}
	const body = "body TEXT NOT NULL DEFAULT '',"
	want := strings.Replace(create, body, body+" kind TEXT NOT NULL DEFAULT '', confirmed_at TEXT NOT NULL DEFAULT '',", 1)
	if got := createSQL(t, ib.conn, "decisions"); got != want {
		t.Fatalf("decisions:\n%s", got)
	}
	// An old Done/Later ask is not a manual one: no kind, no confirm.
	for _, id := range []string{"7.1", "7.2"} {
		if d := get(t, ib, id); d.Kind != "" || d.ConfirmedAt != "" {
			t.Fatalf("%+v", d)
		}
	}
	if d := get(t, ib, "7.1"); d.Body != "steps" {
		t.Fatalf("%+v", d)
	}
	if line := AnswerLine(get(t, ib, "7.2"), -1); line != "7.2 Done? → a: Done" {
		t.Fatalf("%q", line)
	}
	_ = ib.Close()

	// A v11 binary sees user_version 12 as current: it reads and writes its own columns.
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if got := tableRows(t, raw, "decisions", v11Columns); got != before {
		t.Fatalf("rows changed:\n%s\n%s", got, before)
	}
	if _, err := raw.Exec("INSERT INTO decisions (id, task, seq, session_id, session_name, question, body, created_at, updated_at)" +
		" VALUES ('7.3', '7', 3, 's1', 'spec-s1', 'From v11?', '', 'x', 'x')"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	ib, err = Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer ib.Close()
	if d := get(t, ib, "7.3"); d.Kind != "" || d.Question != "From v11?" {
		t.Fatalf("%+v", d)
	}
	d, err := ib.Ask(AskInput{Task: "7", Question: "Run it?", Manual: true, SessionID: "s1", Name: "spec-s1"})
	if err != nil || d.ID != "7.4" || d.Kind != KindManual {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestManualAskHasFixedOptions(t *testing.T) {
	ib, _, _ := open(t)
	d := askManual(t, ib, AskInput{Body: "```sh\njust rotate\n```"})
	if d.Kind != KindManual || d.Rec != "a" || len(d.Options) != 2 || d.OptionText("a") != "Done" || d.OptionText("b") != "Later" {
		t.Fatalf("%+v", d)
	}
	if got := jsonKeys(d.JSON()); !strings.HasSuffix(got, ",brief,body,kind,confirmed_at") {
		t.Fatalf("%s", got)
	}
	if d := askManual(t, ib, AskInput{Rec: "b"}); d.Rec != "b" {
		t.Fatalf("rec %q", d.Rec)
	}
	plain := get(t, ib, ask(t, ib, "7", "s1", false, "Plain?", "Done", "Later"))
	if plain.Kind != "" || strings.Contains(jsonKeys(plain.JSON()), ",kind") || plain.NeedsConfirm("a") {
		t.Fatalf("a plain Done/Later ask became manual: %+v", plain)
	}
	for _, in := range []AskInput{
		{Options: []string{"x", "y"}},
		{Gate: true},
		{Prod: &ProdAction{}},
		{Rec: "c"},
	} {
		in.Task, in.Question, in.Manual, in.SessionID = "929", "Q?", true, "s1"
		if _, err := ib.Ask(in); code(err) != 2 {
			t.Fatalf("%+v: %v", in, err)
		}
	}
}

func TestManualDoneOnlyWithTheUserBit(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", "2026-11-20T12:00:00Z")
	ib, fake, _ := open(t)
	id := askManual(t, ib, AskInput{}).ID
	_, err := ib.Answer(id, p("a"), nil, "", nil)
	if code(err) != 1 || err.Error() != id+" is a manual-step ask; confirm Done in the decisions panel" {
		t.Fatalf("%v", err)
	}
	if err := ib.CloseRaw(id, "answered", "pick", "a", "", "held"); code(err) != 1 {
		t.Fatalf("CloseRaw: %v", err)
	}
	if d := get(t, ib, id); d.Status != "open" || d.ConfirmedAt != "" || len(fake.sends) != 0 {
		t.Fatalf("%+v sends %q", d, fake.sends)
	}

	ib.User = true
	if _, err := ib.Answer(id, p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	d := get(t, ib, id)
	if d.Status != "answered" || d.AnswerOption != "a" || d.ConfirmedAt != "2026-11-20T12:00:00+00:00" {
		t.Fatalf("%+v", d)
	}
	if len(fake.sends) != 1 || !strings.Contains(fake.sends[0][1], id+" Rotate the API key? → a: Done (confirmed)") {
		t.Fatalf("%q", fake.sends)
	}
}

func TestManualLaterTextAndTalkNeedNoConfirm(t *testing.T) {
	ib, fake, _ := open(t)
	later := askManual(t, ib, AskInput{}).ID
	if _, err := ib.Answer(later, p("b"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	if d := get(t, ib, later); d.AnswerOption != "b" || d.ConfirmedAt != "" {
		t.Fatalf("%+v", d)
	}
	if len(fake.sends) != 1 || !strings.HasSuffix(fake.sends[0][1], "→ b: Later") {
		t.Fatalf("%q", fake.sends)
	}
	text := askManual(t, ib, AskInput{}).ID
	if _, err := ib.Answer(text, nil, p("ran half of it"), "", nil); err != nil {
		t.Fatal(err)
	}
	talk := askManual(t, ib, AskInput{}).ID
	if _, err := ib.Resolve(talk, ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{text, talk} {
		if d := get(t, ib, id); d.Status != "answered" || d.ConfirmedAt != "" {
			t.Fatalf("%+v", d)
		}
	}
}

func TestManualAmendKeepsOptions(t *testing.T) {
	ib, _, _ := open(t)
	id := askManual(t, ib, AskInput{}).ID
	if _, err := ib.Amend(id, "s1", false, AmendInput{Options: []string{"Done", "Never"}}); code(err) != 1 {
		t.Fatalf("%v", err)
	}
	d, err := ib.Amend(id, "s1", false, AmendInput{Question: p("Rotate both keys?"), Body: p("`just rotate`")})
	if err != nil || d.Kind != KindManual || d.OptionText("a") != "Done" || d.Body != "`just rotate`" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestManualDoneNeverPersistedAsDraft(t *testing.T) {
	ib, _, _ := open(t)
	id := askManual(t, ib, AskInput{}).ID
	save(t, ib, Draft{ID: id, Kind: "pick", Option: "b"})
	if got := drafts(t, ib)[id]; got.Option != "b" {
		t.Fatalf("Later not kept: %v", got)
	}
	save(t, ib, Draft{ID: id, Kind: "pick", Option: "a"})
	if got, ok := drafts(t, ib)[id]; ok {
		t.Fatalf("Done persisted, or the stale Later kept: %v", got)
	}
}
