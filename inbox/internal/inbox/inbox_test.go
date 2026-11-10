package inbox

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
)

type fakeAM struct {
	mu    sync.Mutex
	sends [][2]string
	fail  string
	// followup is what Followup reports, and followed counts its calls.
	followup string
	followed int
}

func (f *fakeAM) Sessions() am.Sessions { return nil }
func (f *fakeAM) Popup(string) bool     { return true }
func (f *fakeAM) Bell(string)           {}

func (f *fakeAM) Followup(string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.followed++
	return f.followup
}

func (f *fakeAM) Send(sid, msg string) (bool, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, [2]string{sid, msg})
	if f.fail != "" {
		return false, "", f.fail
	}
	return true, "42", ""
}

func open(t *testing.T) (*Inbox, *fakeAM, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	fake := &fakeAM{}
	ib, err := Open(path, fake)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ib.Close() })
	return ib, fake, path
}

func ask(t *testing.T, ib *Inbox, task, sid string, gate bool, q string, opts ...string) string {
	t.Helper()
	if opts == nil {
		opts = []string{"left", "right"}
	}
	if len(opts) == 1 && opts[0] == "" {
		opts = nil
	}
	d, err := ib.Ask(AskInput{Task: task, Question: q, Options: opts, Gate: gate, SessionID: sid, Name: "spec-" + sid})
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func p(s string) *string { return &s }

func code(err error) int {
	var ie *Error
	if errors.As(err, &ie) {
		return ie.Code
	}
	return -1
}

func get(t *testing.T, ib *Inbox, id string) Decision {
	t.Helper()
	d, err := ib.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestIDsSequentialPerTaskAndNeverReused(t *testing.T) {
	ib, _, _ := open(t)
	got := []string{ask(t, ib, "7", "s1", false, "Q?"), ask(t, ib, "7", "s1", false, "Q?"), ask(t, ib, "8", "s1", false, "Q?")}
	if _, err := ib.Withdraw("7.2", "s1", false); err != nil {
		t.Fatal(err)
	}
	got = append(got, ask(t, ib, "7", "s1", false, "Q?"))
	for range 3 {
		got = append(got, ask(t, ib, "7", "s1", true, "Q?"))
	}
	got = append(got, ask(t, ib, "7", "s1", false, "Q?"))
	want := []string{"7.1", "7.2", "8.1", "7.3", "7.g", "7.g2", "7.g3", "7.4"}
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestSchemaInitAndReopen(t *testing.T) {
	ib, fake, path := open(t)
	ask(t, ib, "7", "s1", false, "Q?")
	var mode string
	if err := ib.conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode %q %v", mode, err)
	}
	_ = ib.Close()
	again, err := Open(path, fake)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if v, _ := again.userVersion(); v != SchemaVersion {
		t.Fatalf("user_version %d", v)
	}
	if ds, _ := again.Select("1"); len(ds) != 1 || ds[0].ID != "7.1" {
		t.Fatalf("%v", ds)
	}
}

func v1DB(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, stmt := range strings.Split(Migrations[1], ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, err := raw.Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, stmt := range []string{
		"PRAGMA user_version = 1",
		"INSERT INTO decisions (id, task, seq, session_id, session_name, question, created_at, updated_at)" +
			" VALUES ('7.1', '7', 1, 's1', 'spec-s1', 'Old?', '2026-01-01T00:00:00+00:00', '2026-01-01T00:00:00+00:00')",
		"INSERT INTO decision_options VALUES ('7.1', 'a', 'x'), ('7.1', 'b', 'y')",
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func schema(t *testing.T, ib *Inbox) []string {
	t.Helper()
	rows, err := ib.conn.QueryContext(ctx, "SELECT sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestV1DBMigratesToV2Once(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	v1DB(t, path)
	ib, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := ib.userVersion(); v != 2 {
		t.Fatalf("user_version %d", v)
	}
	d := get(t, ib, "7.1")
	if d.Question != "Old?" || len(d.Options) != 2 || d.Options[1] != (Option{"b", "y"}) || d.Title != "" {
		t.Fatalf("%+v", d)
	}
	before := schema(t, ib)
	_ = ib.Close()
	again, err := Open(path, &fakeAM{})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if !slices.Equal(schema(t, again), before) {
		t.Fatal("reopen changed the schema")
	}
}

func TestOptionCountAndRecRejected(t *testing.T) {
	ib, _, _ := open(t)
	for _, opts := range [][]string{{"a"}, {"a", "b", "c", "d", "e"}, {"a", " "}} {
		if _, err := ib.Ask(AskInput{Task: "7", Question: "Q?", Options: opts, SessionID: "s1"}); code(err) != 2 {
			t.Errorf("%v: %v", opts, err)
		}
	}
	if _, err := ib.Ask(AskInput{Task: "7", Question: "Q?", Options: []string{"a", "b"}, Rec: "c", SessionID: "s1"}); code(err) != 2 {
		t.Error(err)
	}
	if _, err := ib.Ask(AskInput{Task: "x y", Question: "Q?", SessionID: "s1"}); code(err) != 2 {
		t.Error(err)
	}
}

func TestAmendOwnerOnlyAndForce(t *testing.T) {
	ib, _, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?")
	if _, err := ib.Amend("7.1", "s2", false, AmendInput{Question: p("New?")}); code(err) != 1 {
		t.Fatal(err)
	}
	if _, err := ib.Amend("7.1", "s2", true, AmendInput{Question: p("New?")}); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Amend("7.1", "s1", false, AmendInput{Options: []string{"x", "y", "z"}, Rec: p("c")}); err != nil {
		t.Fatal(err)
	}
	d := get(t, ib, "7.1")
	if d.Question != "New?" || d.Rec != "c" || !d.Amended || d.OptionText("c") != "z" {
		t.Fatalf("%+v", d)
	}
	if _, err := ib.Amend("7.1", "s1", false, AmendInput{Options: []string{"x", "y"}}); err != nil {
		t.Fatal(err)
	}
	if d := get(t, ib, "7.1"); d.Rec != "" {
		t.Fatalf("rec %q should drop with its option", d.Rec)
	}
}

func TestWithdrawOwnerOnlyAndClosedRefused(t *testing.T) {
	ib, _, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?")
	ask(t, ib, "7", "s1", false, "Q?")
	if _, err := ib.Withdraw("7.1", "s2", false); code(err) != 1 {
		t.Fatal(err)
	}
	if _, err := ib.Withdraw("7.1", "s2", true); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Answer("7.2", p("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"7.1", "7.2"} {
		if _, err := ib.Amend(id, "s1", true, AmendInput{Question: p("x?")}); code(err) != 1 {
			t.Errorf("amend %s: %v", id, err)
		}
		if _, err := ib.Withdraw(id, "s1", true); code(err) != 1 {
			t.Errorf("withdraw %s: %v", id, err)
		}
		if _, err := ib.Answer(id, p("b"), nil, "", nil); code(err) != 1 {
			t.Errorf("answer %s: %v", id, err)
		}
		if _, err := ib.Resolve(id, ""); code(err) != 1 {
			t.Errorf("resolve %s: %v", id, err)
		}
	}
	if _, err := ib.Get("7.99"); code(err) != 1 {
		t.Fatal(err)
	}
}

func TestFirstAnswerHeldSecondSendsOneMessage(t *testing.T) {
	ib, fake, _ := open(t)
	ask(t, ib, "7", "s1", false, "First?")
	ask(t, ib, "7", "s1", false, "Second?")
	if res, err := ib.Answer("7.2", nil, p("do it"), "", nil); res != nil || err != nil {
		t.Fatal(res, err)
	}
	if get(t, ib, "7.2").Delivery != "held" || len(fake.sends) != 0 {
		t.Fatal("not held")
	}
	res, err := ib.Answer("7.1", p("b"), nil, "", nil)
	if err != nil || !res.OK || len(fake.sends) != 1 {
		t.Fatal(res, err, fake.sends)
	}
	msg := fake.sends[0][1]
	if strings.Index(msg, "7.1 First?") > strings.Index(msg, "7.2 Second?") {
		t.Fatal(msg)
	}
	for _, id := range []string{"7.1", "7.2"} {
		if d := get(t, ib, id); d.Delivery != "sent" || d.MessageID != "42" {
			t.Fatalf("%+v", d)
		}
	}
}

func TestResolveOrForcedWithdrawOfLastOpenFlushes(t *testing.T) {
	for _, closer := range []string{"resolve", "withdraw"} {
		ib, fake, _ := open(t)
		ask(t, ib, "7", "s1", false, "Answered?")
		ask(t, ib, "7", "s1", false, "Closer?")
		if _, err := ib.Answer("7.1", p("a"), nil, "", nil); err != nil {
			t.Fatal(err)
		}
		var res *Delivery
		var err error
		if closer == "resolve" {
			res, err = ib.Resolve("7.2", "talked it over")
		} else {
			res, err = ib.Withdraw("7.2", "chief", true)
		}
		if err != nil || res == nil || !slices.Equal(res.IDs, []string{"7.1"}) || res.Message != "" {
			t.Fatalf("%s: %+v %v", closer, res, err)
		}
		if len(fake.sends) != 1 || !strings.Contains(fake.sends[0][1], "Answered?") ||
			strings.Contains(fake.sends[0][1], "Closer?") || get(t, ib, "7.2").Delivery != "none" {
			t.Fatalf("%s: %v", closer, fake.sends)
		}
	}
}

func TestSelfWithdrawHandsBackInsteadOfSending(t *testing.T) {
	ib, fake, _ := open(t)
	ask(t, ib, "7", "s1", false, "Answered?")
	ask(t, ib, "7", "s1", false, "Withdrawn?")
	if _, err := ib.Answer("7.1", p("b"), nil, "ship it", nil); err != nil {
		t.Fatal(err)
	}
	res, err := ib.Withdraw("7.2", "s1", false)
	if err != nil || res == nil {
		t.Fatal(res, err)
	}
	if len(fake.sends) != 0 {
		t.Fatalf("self-withdraw sent %v", fake.sends)
	}
	want := Header + "\n7.1 Answered? → b: right\n  Note: ship it"
	if res.Message != want || !res.OK {
		t.Fatalf("%+v", res)
	}
	if d := get(t, ib, "7.1"); d.Delivery != "sent" || d.MessageID != "" || d.DeliveryError != "" {
		t.Fatalf("%+v", d)
	}
	if res, err := ib.Withdraw(ask(t, ib, "8", "s1", false, "Alone?"), "s1", false); res != nil || err != nil {
		t.Fatalf("nothing held should hand back nothing: %+v %v", res, err)
	}
}

func TestOtherSessionsDoNotHold(t *testing.T) {
	ib, fake, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?")
	ask(t, ib, "7", "s2", false, "Q?")
	if res, err := ib.Answer("7.1", p("a"), nil, "", nil); err != nil || !res.OK || fake.sends[0][0] != "s1" {
		t.Fatal(res, err)
	}
}

func TestResolveAloneSendsNothing(t *testing.T) {
	ib, fake, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?")
	if res, err := ib.Resolve("7.1", ""); res != nil || err != nil || len(fake.sends) != 0 {
		t.Fatal(res, err)
	}
	if get(t, ib, "7.1").AnswerKind != "talk" {
		t.Fatal("not talk")
	}
}

func TestMessageLines(t *testing.T) {
	ib, fake, _ := open(t)
	ask(t, ib, "7", "s1", false, "Pick?")
	ask(t, ib, "7", "s1", false, "Free?", "")
	if _, err := ib.Answer("7.1", p("b"), nil, "ship it", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Answer("7.2", nil, p("use the env var"), "", nil); err != nil {
		t.Fatal(err)
	}
	want := "[zen-inbox] Answers to your decisions:\n7.1 Pick? → b: right\n  Note: ship it\n" +
		"7.2 Free? → free text: use the env var"
	if fake.sends[0] != [2]string{"s1", want} {
		t.Fatalf("%q", fake.sends)
	}
}

func held(t *testing.T, ib *Inbox, ids ...string) []Decision {
	t.Helper()
	for _, id := range ids {
		if err := ib.CloseRaw(id, "answered", "pick", "a", "", "held"); err != nil {
			t.Fatal(err)
		}
	}
	ds, err := ib.Select("delivery = 'held'")
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestOversizeBatchTrimmedUnderLimit(t *testing.T) {
	ib, _, _ := open(t)
	for range 4 {
		ask(t, ib, "7", "s1", false, strings.Repeat("Q", 3000))
	}
	msg := BuildMessage(held(t, ib, "7.1", "7.2", "7.3", "7.4"), SendLimit)
	if len(msg) >= SendLimit {
		t.Fatal(len(msg))
	}
	for _, id := range []string{"7.1", "7.2", "7.3", "7.4"} {
		if !strings.Contains(msg, id) {
			t.Fatal(id)
		}
	}
	if strings.Count(msg, "→ a: left") != 4 {
		t.Fatal(msg)
	}
}

func TestHugeFreeTextHardTruncatedAtUTF8Boundary(t *testing.T) {
	ib, _, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?", "")
	if err := ib.CloseRaw("7.1", "answered", "text", "", strings.Repeat("é", 9000), "held"); err != nil {
		t.Fatal(err)
	}
	ds, _ := ib.Select("1")
	msg := BuildMessage(ds, SendLimit)
	if len(msg) >= SendLimit || !strings.HasSuffix(msg, "zen-inbox show <id>") || !utf8.ValidString(msg) {
		t.Fatal(len(msg))
	}
}

func TestMessageIDsInNumericOrder(t *testing.T) {
	ib, _, _ := open(t)
	for range 10 {
		ask(t, ib, "7", "s1", false, "Q?")
	}
	msg := BuildMessage(held(t, ib, "7.10", "7.2"), SendLimit)
	if strings.Index(msg, "7.2 ") > strings.Index(msg, "7.10 ") {
		t.Fatal(msg)
	}
}

func TestFollowupKeepsDeliverySentAndDescribes(t *testing.T) {
	for outcome, want := range map[string]string{
		"":            "sent 7.1 to spec-s1",
		am.Submitted:  "sent 7.1 to spec-s1 (submitted a stuck paste)",
		am.StillStuck: "sent 7.1 to spec-s1; the paste is still stuck, attach: tmux -L agentmgr attach -t am_s1",
	} {
		ib, fake, _ := open(t)
		fake.followup = outcome
		ask(t, ib, "7", "s1", false, "Q?")
		res, err := ib.Answer("7.1", p("a"), nil, "", nil)
		if err != nil || !res.OK || res.Followup != outcome || fake.followed != 1 {
			t.Fatal(outcome, res, err, fake.followed)
		}
		if d := get(t, ib, "7.1"); d.Delivery != "sent" || d.DeliveryError != "" {
			t.Fatalf("%q: %+v", outcome, d)
		}
		if got, err := ib.Describe(res, "s1", "spec-s1"); err != nil || got != want {
			t.Fatalf("%q: %q %v", outcome, got, err)
		}
	}
}

func TestFailedSendSkipsFollowup(t *testing.T) {
	ib, fake, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?")
	fake.fail = "send refused"
	if _, err := ib.Answer("7.1", p("a"), nil, "", nil); err != nil || fake.followed != 0 {
		t.Fatal(err, fake.followed)
	}
}

func TestAnswerInsideAskingSessionSendsNothing(t *testing.T) {
	ib, fake, _ := open(t)
	ib.Self = "s1"
	ask(t, ib, "7", "s1", false, "Q?")
	res, err := ib.Answer("7.1", p("a"), nil, "", nil)
	if err != nil || !res.OK || res.Message == "" || len(fake.sends) != 0 || fake.followed != 0 {
		t.Fatal(res, err, fake.sends)
	}
	if d := get(t, ib, "7.1"); d.Delivery != "sent" || d.DeliveryError != "" {
		t.Fatalf("%+v", d)
	}
	if got, _ := ib.Describe(res, "s1", ""); !strings.HasPrefix(got, "recorded 7.1") {
		t.Fatal(got)
	}
}

func TestFailedSendThenRetry(t *testing.T) {
	ib, fake, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?")
	fake.fail = "send refused"
	res, err := ib.Answer("7.1", p("a"), nil, "", nil)
	if err != nil || res.OK || res.Error != "send refused" {
		t.Fatal(res, err)
	}
	if d := get(t, ib, "7.1"); d.Delivery != "failed" || d.DeliveryError != "send refused" {
		t.Fatalf("%+v", d)
	}
	fake.fail = ""
	if res, err := ib.Retry("spec-s1"); err != nil || !res.OK {
		t.Fatal(res, err)
	}
	if fake.sends[0][1] != fake.sends[1][1] || get(t, ib, "7.1").Delivery != "sent" {
		t.Fatal(fake.sends)
	}
	if _, err := ib.Retry("s1"); code(err) != 1 {
		t.Fatal(err)
	}
}

func TestInFlightSendingNotRetryableUntilStale(t *testing.T) {
	ib, fake, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?")
	if err := ib.Exec("UPDATE decisions SET status = 'answered', answer_kind = 'pick', answer_option = 'a',"+
		" delivery = 'sending', updated_at = ?", Stamp(Now())); err != nil {
		t.Fatal(err)
	}
	if b, _ := ib.Undelivered(); len(b) != 0 {
		t.Fatal(b)
	}
	if _, err := ib.Retry("s1"); code(err) != 1 {
		t.Fatal(err)
	}
	if err := ib.Exec("UPDATE decisions SET updated_at = '2000-01-01T00:00:00+00:00'"); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Retry("s1"); err != nil || len(fake.sends) != 1 {
		t.Fatal(err)
	}
}

func TestHeldWithNothingOpenIsRetryable(t *testing.T) {
	ib, _, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?")
	held(t, ib, "7.1")
	if b, _ := ib.Undelivered(); len(b) != 1 || b[0].SessionID != "s1" {
		t.Fatal(b)
	}
	if res, err := ib.Retry("s1"); err != nil || !res.OK {
		t.Fatal(err)
	}
}

func TestPickCheckedAgainstCurrentOptionsAndRev(t *testing.T) {
	ib, _, _ := open(t)
	ask(t, ib, "7", "s1", false, "Q?", "x", "y", "z")
	rev := get(t, ib, "7.1").Rev
	if _, err := ib.Amend("7.1", "s1", false, AmendInput{Options: []string{"x", "y"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Answer("7.1", p("c"), nil, "", nil); code(err) != 2 {
		t.Fatal(err)
	}
	var ie *Error
	if _, err := ib.Answer("7.1", p("a"), nil, "", &rev); !errors.As(err, &ie) || !ie.Amended || ie.Msg != "7.1 was amended" {
		t.Fatal(err)
	}
	if get(t, ib, "7.1").Status != "open" {
		t.Fatal("closed")
	}
	if _, err := ib.Answer("7.1", nil, nil, "", nil); code(err) != 2 {
		t.Fatal(err)
	}
	if _, err := ib.Answer("7.1", p("a"), p("x"), "", nil); code(err) != 2 {
		t.Fatal(err)
	}
	if _, err := ib.Answer("7.1", nil, p("  "), "", nil); code(err) != 2 {
		t.Fatal(err)
	}
}

func TestBriefUpsertKeepsUnsetFields(t *testing.T) {
	ib, _, _ := open(t)
	if _, err := ib.Ask(AskInput{Task: "7", Question: "Q?", SessionID: "s1", Title: "Old", Brief: "First"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Ask(AskInput{Task: "7", Question: "Q?", SessionID: "s1", Title: "New"}); err != nil {
		t.Fatal(err)
	}
	if d := get(t, ib, "7.2"); d.Title != "New" || d.Brief != "First" {
		t.Fatalf("%+v", d)
	}
	if _, err := ib.SetBrief("s1", "7", nil, p("")); err != nil {
		t.Fatal(err)
	}
	if d := get(t, ib, "7.1"); d.Title != "New" || d.Brief != "" {
		t.Fatalf("%+v", d)
	}
	if _, err := ib.SetBrief("s1", "7", nil, nil); code(err) != 2 {
		t.Fatal(err)
	}
}

func TestCompareID(t *testing.T) {
	ids := []string{"7.g2", "10.1", "7.10", "abc.1", "7.g", "7.2", "007.3"}
	SortIDs(ids)
	if want := []string{"7.2", "007.3", "7.10", "7.g", "7.g2", "10.1", "abc.1"}; !slices.Equal(ids, want) {
		t.Fatal(ids)
	}
}
