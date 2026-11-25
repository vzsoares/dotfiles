package inbox

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
)

// failGone answers task for s1 while s1 is archived, leaving it failed.
func failGone(t *testing.T, ib *Inbox, fake *fakeAM, extra ...am.Session) {
	t.Helper()
	fake.sessions = append(am.Sessions{{"id": "s1", "name": "spec-s1", "running": true, "archived": true}}, extra...)
	if res := answerTo(t, ib, "7", "s1"); res.OK {
		t.Fatalf("%+v", res)
	}
	if d := get(t, ib, "7.1"); d.Delivery != "failed" || !strings.Contains(d.DeliveryError, "is gone") {
		t.Fatalf("%+v", d)
	}
}

func TestRetryToChiefSendsAGoneAskersAnswers(t *testing.T) {
	ib, fake, _ := open(t)
	failGone(t, ib, fake, session("c1", "chief", "idle"))
	res, err := ib.RetryToChief("s1")
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if len(fake.sends) != 1 || fake.sends[0][0] != "c1" {
		t.Fatalf("%q", fake.sends)
	}
	msg := fake.sends[0][1]
	if !strings.HasPrefix(msg, "[zen-inbox] These answers were meant for spec-s1 (s1), sent to you instead:") ||
		!strings.Contains(msg, Header) || !strings.Contains(msg, "7.1") {
		t.Fatalf("%q", msg)
	}
	if d := get(t, ib, "7.1"); d.Delivery != "sent" || d.MessageID != "42" || d.DeliveryError != "" {
		t.Fatalf("%+v", d)
	}
	if failed, _ := ib.Undelivered(); len(failed) != 0 {
		t.Fatalf("%+v", failed)
	}
}

func TestToChiefHeaderCallsAProdApprovalANotice(t *testing.T) {
	if h := ToChiefHeader("n", "s", true); !strings.Contains(h, "notice only") {
		t.Fatalf("%q", h)
	}
	if h := ToChiefHeader("n", "s", false); strings.Contains(h, "notice") {
		t.Fatalf("%q", h)
	}
}

func TestRetryToChiefRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		extra   []am.Session
		wantErr string
	}{
		"no chief":         {wantErr: "found 0"},
		"two chiefs":       {extra: []am.Session{session("c1", "chief", "idle"), session("c2", "chief", "idle")}, wantErr: "found 2"},
		"chief not listed": {extra: []am.Session{session("c1", "chief", "dead")}, wantErr: "found 0"},
	} {
		t.Run(name, func(t *testing.T) {
			ib, fake, _ := open(t)
			failGone(t, ib, fake, tc.extra...)
			before := get(t, ib, "7.1")
			res, err := ib.RetryToChief("s1")
			if err == nil || res != nil || !strings.Contains(err.Error(), tc.wantErr) || code(err) == 0 {
				t.Fatalf("%+v %v", res, err)
			}
			if len(fake.sends) != 0 || !reflect.DeepEqual(get(t, ib, "7.1"), before) {
				t.Fatalf("rows or sends changed: %q %+v", fake.sends, get(t, ib, "7.1"))
			}
		})
	}
}

func TestRetryToChiefLeavesTheRowsWhenTheSendFails(t *testing.T) {
	ib, fake, _ := open(t)
	failGone(t, ib, fake, session("c1", "chief", "idle"))
	before := get(t, ib, "7.1")
	fake.fail = "boom"
	if _, err := ib.RetryToChief("s1"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal(err)
	}
	after := get(t, ib, "7.1")
	after.Rev, after.UpdatedAt = before.Rev, before.UpdatedAt
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("%+v", after)
	}
}

func TestRetryToChiefWithNothingFailed(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	if _, err := ib.RetryToChief("s1"); err == nil || code(err) != 1 {
		t.Fatal(err)
	}
}

func TestRetryToChiefWorksWhateverKeptTheAnswerFromItsAsker(t *testing.T) {
	for name, later := range map[string]am.Sessions{
		"asker back":    {session("s1", "spec-s1", "idle")},
		"live namesake": {session("s9", "spec-s1", "idle")},
	} {
		t.Run(name, func(t *testing.T) {
			ib, fake, _ := open(t)
			failGone(t, ib, fake, session("c1", "chief", "idle"))
			fake.sessions = append(fake.sessions, later...)
			fake.sends = nil
			res, err := ib.RetryToChief("s1")
			if err != nil || !res.OK || len(fake.sends) != 1 || fake.sends[0][0] != "c1" {
				t.Fatalf("%+v %v %q", res, err, fake.sends)
			}
			if d := get(t, ib, "7.1"); d.Delivery != "sent" {
				t.Fatalf("%+v", d)
			}
		})
	}
}

// refusedEntry records a refusal for s1 and returns its entry.
func refusedEntry(t *testing.T, ib *Inbox, fake *fakeAM, extra ...am.Session) Entry {
	t.Helper()
	fake.sessions = append(am.Sessions{{"id": "s1", "name": "spec-s1", "running": true, "archived": true}}, extra...)
	if err := ib.NoteRefusal("answers:s1:7.1", "s1", "spec-s1", "the answer", "nope"); err != nil {
		t.Fatal(err)
	}
	es, err := ib.Entries(false)
	if err != nil || len(es) != 1 {
		t.Fatalf("%v %+v", err, es)
	}
	return es[0]
}

func TestResendToChiefSendsTheBodyAndClosesTheEntry(t *testing.T) {
	ib, fake, _ := open(t)
	e := refusedEntry(t, ib, fake, session("c1", "chief", "idle"))
	_, text, err := ib.ResendToChief(e.ID)
	if err != nil || !strings.Contains(text, "to chief") {
		t.Fatalf("%q %v", text, err)
	}
	if len(fake.sends) != 1 || fake.sends[0][0] != "c1" ||
		!strings.Contains(fake.sends[0][1], "(resent from the cockpit: ") ||
		!strings.Contains(fake.sends[0][1], "meant for spec-s1 (s1)") ||
		!strings.HasSuffix(fake.sends[0][1], ") the answer") {
		t.Fatalf("%q", fake.sends)
	}
	if got, _ := ib.Entry(e.ID); got.State != "resolved" || got.Resolution != "resent" {
		t.Fatalf("%+v", got)
	}
}

func TestResendToChiefRefusals(t *testing.T) {
	for name, extra := range map[string][]am.Session{
		"no chief":   nil,
		"two chiefs": {session("c1", "chief", "idle"), session("c2", "chief", "idle")},
	} {
		t.Run(name, func(t *testing.T) {
			ib, fake, _ := open(t)
			e := refusedEntry(t, ib, fake, extra...)
			if _, _, err := ib.ResendToChief(e.ID); err == nil || code(err) == 0 || !strings.Contains(err.Error(), "found") {
				t.Fatal(err)
			}
			if got, _ := ib.Entry(e.ID); !got.Open() || len(fake.sends) != 0 {
				t.Fatalf("%+v %q", got, fake.sends)
			}
		})
	}
}
