package inbox

import (
	"strings"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
)

const t0 = "2026-11-24T15:43:00Z"

// advance moves the clock d past its current value.
func advance(t *testing.T, d time.Duration) {
	t.Helper()
	t.Setenv("ZEN_INBOX_NOW", Now().Add(d).Format(time.RFC3339))
}

func TestRateLimitedAnswerIsRetriedUntilSent(t *testing.T) {
	at(t, t0)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle")}
	fake.failFirst = 2
	res := answerTo(t, ib, "7", "s1")
	if res.OK || !res.Waiting || !strings.HasPrefix(res.Error, "retrying (attempt 1") {
		t.Fatalf("%+v", res)
	}
	d := get(t, ib, "7.1")
	if d.Delivery != "waiting" || !strings.Contains(d.DeliveryError, rateLimited) {
		t.Fatalf("%+v", d)
	}
	if failed, _ := ib.Undelivered(); len(failed) != 0 {
		t.Fatalf("a retrying answer is listed as failed: %+v", failed)
	}
	// Inside the backoff nothing is sent.
	pass(t, ib)
	advance(t, 14*time.Second)
	pass(t, ib)
	if fake.failFirst != 1 {
		t.Fatalf("the pass retried inside the 15 s backoff: %d", fake.failFirst)
	}
	advance(t, 2*time.Second)
	pass(t, ib)
	if d = get(t, ib, "7.1"); d.Delivery != "waiting" || !strings.HasPrefix(d.DeliveryError, "retrying (attempt 2") {
		t.Fatalf("%+v", d)
	}
	advance(t, 31*time.Second)
	pass(t, ib)
	if d = get(t, ib, "7.1"); d.Delivery != "sent" || len(fake.sends) != 1 {
		t.Fatalf("%+v sends %d", d, len(fake.sends))
	}
}

func TestRateLimitedAnswerGivesUpAfterThirtyMinutes(t *testing.T) {
	at(t, t0)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle")}
	fake.failFirst = 1000
	answerTo(t, ib, "7", "s1")
	for range 40 {
		advance(t, time.Minute)
		pass(t, ib)
	}
	d := get(t, ib, "7.1")
	if d.Delivery != "failed" || !strings.HasPrefix(d.DeliveryError, "gave up after") {
		t.Fatalf("%+v", d)
	}
	if failed, _ := ib.Undelivered(); len(failed) != 1 {
		t.Fatalf("a given-up answer keeps its retry key: %+v", failed)
	}
}

func TestRateLimitedChatIsRetriedUntilSent(t *testing.T) {
	at(t, t0)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	fake.failFirst = 1
	c, err := ib.SendChat("", "are you there")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != "waiting" || !IsDeferred(c.Error) {
		t.Fatalf("%+v", c)
	}
	advance(t, 16*time.Second)
	pass(t, ib)
	c = chat(t, ib, c.ID)
	if c.Status != "queued" || len(fake.chiefs) != 1 {
		t.Fatalf("%+v chiefs %q", c, fake.chiefs)
	}
	if strings.Contains(fake.chiefs[0], "chief was down") {
		t.Fatalf("a retried chat says chief was down: %q", fake.chiefs[0])
	}
}

func TestSixSendsToOneSessionAllGoOutWithoutARefusal(t *testing.T) {
	at(t, t0)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("s1", "spec-s1", "idle")}
	fake.enforce = true
	for i := range 6 {
		answerTo(t, ib, "7"+string(rune('0'+i)), "s1")
	}
	for range 10 {
		advance(t, 20*time.Second)
		pass(t, ib)
	}
	if fake.refused != 0 {
		t.Fatalf("agent-manager refused %d sends", fake.refused)
	}
	if waiting, _ := ib.Waiting(); len(waiting) != 0 {
		t.Fatalf("still waiting: %+v", waiting)
	}
	for i := range 6 {
		if d := get(t, ib, "7"+string(rune('0'+i))+".1"); d.Delivery != "sent" {
			t.Fatalf("%s: %+v", d.ID, d)
		}
	}
}

func TestChatWaitsForTheWindowInsteadOfFailing(t *testing.T) {
	at(t, t0)
	ib, fake, _ := open(t)
	fake.sessions = am.Sessions{session("c1", "chief", "idle")}
	fake.enforce = true
	var last Chat
	for range 6 {
		advance(t, 5*time.Second)
		c, err := ib.SendChat("", "msg")
		if err != nil {
			t.Fatal(err)
		}
		last = c
	}
	for range 10 {
		advance(t, 20*time.Second)
		pass(t, ib)
	}
	if fake.refused != 0 || len(fake.chiefs) != 6 {
		t.Fatalf("refused %d, chiefs %d", fake.refused, len(fake.chiefs))
	}
	if c := chat(t, ib, last.ID); c.Status == "failed" {
		t.Fatalf("%+v", c)
	}
}
