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
	if d := get(t, ib, "7.1"); d.Delivery != "failed" || !am.IsGone(d.DeliveryError) {
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
	if !strings.HasPrefix(msg, "[zen-inbox] These answers were meant for spec-s1 (s1), which is gone:") ||
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
	live := session("s1", "spec-s1", "idle")
	for name, tc := range map[string]struct {
		extra   []am.Session
		revive  bool
		later   am.Session
		wantErr string
	}{
		"live asker":       {extra: []am.Session{session("c1", "chief", "idle")}, revive: true, wantErr: "is live"},
		"live namesake":    {extra: []am.Session{session("c1", "chief", "idle")}, later: session("s9", "spec-s1", "idle"), wantErr: "is live as s9"},
		"no chief":         {wantErr: "found 0"},
		"two chiefs":       {extra: []am.Session{session("c1", "chief", "idle"), session("c2", "chief", "idle")}, wantErr: "found 2"},
		"chief not listed": {extra: []am.Session{session("c1", "chief", "dead")}, wantErr: "found 0"},
	} {
		t.Run(name, func(t *testing.T) {
			ib, fake, _ := open(t)
			failGone(t, ib, fake, tc.extra...)
			if tc.revive {
				fake.sessions = append(am.Sessions{live}, tc.extra...)
			}
			if tc.later != nil {
				fake.sessions = append(fake.sessions, tc.later)
			}
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
	if !reflect.DeepEqual(get(t, ib, "7.1"), before) {
		t.Fatalf("%+v", get(t, ib, "7.1"))
	}
}

func TestRetryToChiefWithNothingFailed(t *testing.T) {
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	if _, err := ib.RetryToChief("s1"); err == nil || code(err) != 1 {
		t.Fatal(err)
	}
}
