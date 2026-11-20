package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

const longCmd = "aws s3 sync ./dist s3://example-bucket/app --delete --exclude '*.map' --cache-control max-age=31536000,immutable"

const askBody = "**Three commands**, run in this order:\n\n" +
	"1. Build: `just build`\n" +
	"2. Upload:\n\n" +
	"```sh\n" + longCmd + "\n```\n\n" +
	"3. Check:\n\n" +
	"```sh\ncurl -s -o /dev/null \\\n  -w \"%{http_code}\\n\" https://example.com/health\n```\n\n" +
	"- Rollback is `just rollback`.\n"

const checkBlock = "curl -s -o /dev/null \\\n  -w \"%{http_code}\\n\" https://example.com/health"

func (d *drv) askBody() inbox.Decision {
	d.t.Helper()
	dec, err := d.other.Ask(inbox.AskInput{Task: "926", Question: "Run the deploy steps now?", Body: askBody,
		Context: "The bucket is the staging one.", Options: []string{"Run: an agent runs all three", "Hold: nothing runs"},
		Rec: "b", SessionID: "s1", Name: "spec-s1", Title: "Deploy steps"})
	if err != nil {
		d.t.Fatal(err)
	}
	return dec
}

func TestAskBodyRendersUnderQuestionAt92x29(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.askBody()
	d.openWalk()
	d.fits()
	v := d.view()
	q, body, ctx, opt := strings.Index(v, "Run the deploy steps now?"), strings.Index(v, "Three commands"),
		strings.Index(v, "│ The bucket"), strings.Index(v, "a) Run:")
	if q < 0 || body < q || ctx < body || opt < ctx {
		t.Fatalf("order question %d body %d context %d options %d:\n%s", q, body, ctx, opt, v)
	}
	if strings.Contains(v, "**") || strings.Contains(v, "```") {
		t.Fatalf("raw markdown on screen:\n%s", v)
	}
	if !strings.Contains(v, "• Rollback is") || !strings.Contains(v, "[2] copy") {
		t.Fatalf("list or code block missing:\n%s", v)
	}
	// A code line longer than the pane wraps inside its block; nothing is lost.
	if !strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(v, "\n", "")), ""), strings.ReplaceAll(longCmd, " ", "")) {
		t.Fatalf("long command cut:\n%s", v)
	}
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, "curl -s") && strings.ContainsAny(l, "│╭╮╰╯") {
			t.Fatalf("border beside code: %q", l)
		}
	}
	golden(t, "walk-body-92x29", v)
	d.keys("v")
	if d.m.scr != scFull || !strings.Contains(d.view(), "Three commands") {
		t.Fatalf("View full lost the body:\n%s", d.view())
	}
}

func TestAskBodyCopiesBlocksRaw(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.askBody()
	got := d.copies()
	d.openWalk()
	d.keys("y", "1")
	d.keys("y", "2")
	if want := []string{longCmd, checkBlock}; strings.Join(*got, "|") != strings.Join(want, "|") {
		t.Fatalf("copied %q", *got)
	}
	d.keys("esc", "esc")
	d.poll()
	d.keys("enter", "r")
	if d.m.scr != scAll {
		t.Fatalf("not on Review all:\n%s", d.view())
	}
	d.keys("y", "2")
	if len(*got) != 3 || (*got)[2] != checkBlock || !strings.Contains(d.view(), "copied 2") {
		t.Fatalf("Review all copied %q:\n%s", *got, d.view())
	}
}

func TestAskBodyOnSessionAndThreadScreens(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.askBody()
	got := d.copies()
	d.poll()
	d.keys("enter")
	if d.m.scr != scSession || !strings.Contains(d.view(), "Three commands") {
		t.Fatalf("session screen:\n%s", d.view())
	}
	d.keys("y", "1")
	if len(*got) != 1 || (*got)[0] != longCmd {
		t.Fatalf("copied %q", *got)
	}
	d.fits()
	d.keys("esc")
	d.addNote("926", "FYI", "a note makes the thread a header row")
	d.poll()
	d.cursorTo("#926")
	d.keys("o")
	if d.m.scr != scThread || !d.m.thread.loaded || !strings.Contains(d.view(), "Three commands") {
		t.Fatalf("thread screen:\n%s", d.view())
	}
	d.keys("y", "2")
	if len(*got) != 2 || (*got)[1] != checkBlock {
		t.Fatalf("thread copied %q", *got)
	}
	d.fits()
}

func TestAskBodyAmendShowsNewBody(t *testing.T) {
	d := newDrv(t, 92, 29)
	dec := d.askBody()
	d.openWalk()
	if _, err := d.other.Amend(dec.ID, "s1", false, inbox.AmendInput{Body: ptr("Only `just deploy` now.")}); err != nil {
		t.Fatal(err)
	}
	d.poll()
	if v := d.view(); strings.Contains(v, "Three commands") || !strings.Contains(v, "just deploy") {
		t.Fatalf("stale body:\n%s", v)
	}
}

// trickyCmd would be mangled as markdown: emphasis, backticks, runs of spaces.
const trickyCmd = "psql -c 'UPDATE t SET a = a * 2 WHERE b = `x`  AND  c_d = _e_' --set ON_ERROR_STOP=1 ```"

func TestProdRunCommandCopiesExactly(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	for _, cmd := range []string{longRunCmd, trickyCmd} {
		d := newDrvWith(t, 92, 29, func(d *drv) { d.ib.User = true })
		_, err := d.other.AskProd(inbox.AskInput{Task: "736", SessionID: "s1", Name: "spec-s1"},
			inbox.ProdAction{Action: inbox.RunAction, Repo: "core", Cmd: cmd})
		if err != nil {
			t.Fatal(err)
		}
		got := d.copies()
		d.openWalk()
		d.fits()
		if !strings.Contains(d.view(), "[1] copy") {
			t.Fatalf("no copy label:\n%s", d.view())
		}
		d.keys("y", "1")
		if len(*got) != 1 || (*got)[0] != cmd {
			t.Fatalf("copied %q, want %q", *got, cmd)
		}
		if cmd == longRunCmd {
			golden(t, "walk-run-cmd-92x29", d.view())
		}
		d.keys("v")
		d.keys("y", "1")
		if len(*got) != 2 || (*got)[1] != cmd {
			t.Fatalf("View full copied %q", *got)
		}
		d.keys("esc", "esc", "esc")
		d.poll()
		d.keys("enter", "r")
		d.keys("y", "1")
		if d.m.scr != scAll || len(*got) != 3 || (*got)[2] != cmd {
			t.Fatalf("Review all copied %q:\n%s", *got, d.view())
		}
		d.fits()
	}
}

func TestLockedProdRunStillCopies(t *testing.T) {
	d := newDrv(t, 92, 29)
	_, err := d.other.AskProd(inbox.AskInput{Task: "736", SessionID: "s1", Name: "spec-s1"}, runWant())
	if err != nil {
		t.Fatal(err)
	}
	got := d.copies()
	d.openWalk()
	if !strings.Contains(d.view(), "y copy") {
		t.Fatalf("no copy key in the footer:\n%s", d.view())
	}
	d.keys("y", "1")
	if len(*got) != 1 || (*got)[0] != longRunCmd {
		t.Fatalf("copied %q", *got)
	}
}

func TestAskBodyFitsEverySize(t *testing.T) {
	for _, sz := range append(sizes, [2]int{44, 24}) {
		d := newDrv(t, sz[0], sz[1])
		d.askBody()
		d.openWalk()
		d.fits()
		for _, l := range strings.Split(d.view(), "\n") {
			if lipgloss.Width(l) > sz[0] {
				t.Fatalf("%v: wide line %q", sz, l)
			}
		}
		// Every option stays reachable: in view or by scrolling.
		seen := d.view()
		for range 40 {
			d.keys("pgdown")
			seen += d.view()
		}
		if !strings.Contains(seen, "Hold") {
			t.Fatalf("%v: option b never shown:\n%s", sz, d.view())
		}
	}
}
