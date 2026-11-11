package tui

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/md"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

type fakeAM struct {
	mu       sync.Mutex
	sessions am.Sessions
	sends    [][2]string
	popups   []string
	bells    []string
	notices  [][2]string
	noPopup  bool
	fail     string
}

func (f *fakeAM) Sessions() am.Sessions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions
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

func (f *fakeAM) Popup(sid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.popups = append(f.popups, sid)
	return !f.noPopup
}

func (f *fakeAM) Bell(msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bells = append(f.bells, msg)
}

func (f *fakeAM) Notify(title, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, [2]string{title, body})
}

func (f *fakeAM) Followup(string) string { return "" }

func sess(id string) am.Session {
	return am.Session{"id": id, "name": "spec-" + id, "status": "idle", "running": true}
}

type drv struct {
	t     *testing.T
	m     *Model
	ib    *inbox.Inbox
	other *inbox.Inbox
	am    *fakeAM
	quit  bool
}

func newDrv(t *testing.T, w, h int) *drv { return newDrvWith(t, w, h, nil) }

// newDrvWith runs setup before the panel's first read.
func newDrvWith(t *testing.T, w, h int, setup func(*drv)) *drv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zen-flow.db")
	fake := &fakeAM{sessions: am.Sessions{sess("s1"), sess("s2")}}
	ib, err := inbox.Open(path, fake)
	if err != nil {
		t.Fatal(err)
	}
	other, err := inbox.Open(path, fake)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ib.Close(); _ = other.Close() })
	d := &drv{t: t, ib: ib, other: other, am: fake}
	d.m = New(ib, fake)
	d.m.after = func(time.Duration, tea.Msg) tea.Cmd { return nil }
	if setup != nil {
		setup(d)
	}
	d.send(tea.WindowSizeMsg{Width: w, Height: h})
	d.run(d.m.Init())
	return d
}

func (d *drv) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			d.run(c)
		}
	case tea.QuitMsg:
		d.quit = true
	default:
		d.send(msg)
	}
}

func (d *drv) send(msg tea.Msg) {
	_, cmd := d.m.Update(msg)
	d.run(cmd)
}

var special = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEsc, "up": tea.KeyUp, "down": tea.KeyDown,
	"ctrl+d": tea.KeyCtrlD, "ctrl+c": tea.KeyCtrlC, " ": tea.KeySpace, "pgdown": tea.KeyPgDown,
	"pgup": tea.KeyPgUp, "ctrl+u": tea.KeyCtrlU, "left": tea.KeyLeft, "right": tea.KeyRight,
	"shift+tab": tea.KeyShiftTab,
}

func (d *drv) keys(ks ...string) {
	for _, k := range ks {
		if t, ok := special[k]; ok {
			d.send(tea.KeyMsg{Type: t})
		} else {
			d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		}
	}
}

func (d *drv) poll() { d.send(tickMsg{}) }

func (d *drv) view() string { return d.m.View() }

// text is the view with line breaks and indents folded, for finding wrapped text.
func (d *drv) text() string {
	var parts []string
	for _, l := range strings.Split(d.view(), "\n") {
		parts = append(parts, strings.TrimSpace(l))
	}
	return strings.Join(parts, " ")
}

func (d *drv) ask(task, sid, q string, opts ...string) string {
	d.t.Helper()
	dec, err := d.other.Ask(inbox.AskInput{Task: task, Question: q, Options: opts, SessionID: sid, Name: "spec-" + sid})
	if err != nil {
		d.t.Fatal(err)
	}
	return dec.ID
}

func (d *drv) get(id string) inbox.Decision {
	d.t.Helper()
	dec, err := d.ib.Get(id)
	if err != nil {
		d.t.Fatal(err)
	}
	return dec
}

func (d *drv) fits() {
	d.t.Helper()
	lines := strings.Split(d.view(), "\n")
	if len(lines) > d.m.h {
		d.t.Errorf("%d lines in a %d-row pane:\n%s", len(lines), d.m.h, d.view())
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w > d.m.w {
			d.t.Errorf("line %q is %d wide in a %d-column pane", l, w, d.m.w)
		}
	}
}

func words(n int) string {
	const w = "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod "
	return strings.TrimRight(strings.Repeat(w, n/len(w)+1)[:n], " ")
}

func (d *drv) longFixture() (string, string) {
	opt := words(300)
	_, err := d.other.Ask(inbox.AskInput{Task: "570", Question: "Where do briefs live?",
		Options: []string{opt, "keep it in memory"}, Rec: "b", Context: words(400), SessionID: "s1",
		Name: "spec-s1", Title: "Zen board", Brief: strings.Join([]string{words(70), words(70), words(70)}, "\n")})
	if err != nil {
		d.t.Fatal(err)
	}
	return opt, "keep it in memory"
}

func (d *drv) openWalk() {
	d.poll()
	d.keys("enter", "enter")
	if d.m.scr != scWalk {
		d.t.Fatalf("not walking: screen %d\n%s", d.m.scr, d.view())
	}
}

var sizes = [][2]int{{92, 29}, {60, 20}, {40, 15}}

func TestQuestionScreenKeepsQuestionAndOptions(t *testing.T) {
	for _, sz := range sizes {
		// +1 row for the status line, so the screen keeps its old budget.
		d := newDrv(t, sz[0], sz[1]+1)
		a, b := d.longFixture()
		d.openWalk()
		d.fits()
		text := d.text()
		for _, want := range []string{"Where do briefs live?", "a) " + a, "b) " + b + "  ← rec"} {
			if !strings.Contains(text, want) {
				t.Errorf("%dx%d: %q missing from\n%s", sz[0], sz[1], want, d.view())
			}
		}
		f, _ := d.m.walkFit()
		if !slices.Equal(f.cuts, CutOrder[:len(f.cuts)]) || f.scroll {
			t.Errorf("%dx%d: cuts %v scroll %v", sz[0], sz[1], f.cuts, f.scroll)
		}
	}
}

func TestCutOrderAsSpaceShrinks(t *testing.T) {
	d := newDrv(t, 92, 60)
	d.longFixture()
	d.openWalk()
	dec := d.get("570.1")
	b := d.m.brief(dec)
	prev := -1
	clipped := false
	for h := 60; h >= 10; h-- {
		f := fitWalk(dec, b, nil, "", 92, h, len(questionMenu(dec, false)), 1, 0, d.m.paint)
		if len(f.cuts) < prev {
			t.Fatalf("height %d cut %v after %d cuts", h, f.cuts, prev)
		}
		prev = len(f.cuts)
		if h == 60 && len(f.cuts) != 0 {
			t.Fatalf("nothing should be cut at 60 rows: %v", f.cuts)
		}
		for _, l := range f.body {
			clipped = clipped || strings.HasSuffix(l, view.More)
		}
	}
	if !clipped {
		t.Error("context was never clipped with … (View full)")
	}
	if prev < 3 {
		t.Errorf("brief, context and asked-by should be cut at 10 rows, got %d cuts", prev)
	}
}

func TestOverflowingOptionsScrollInsteadOfClipping(t *testing.T) {
	d := newDrv(t, 40, 15)
	opts := []string{words(150), words(150), words(150)}
	d.ask("7", "s1", "Which way?", opts...)
	d.openWalk()
	f, _ := d.m.walkFit()
	if !f.scroll {
		t.Fatalf("expected a scrolling body, cuts %v", f.cuts)
	}
	seen := ""
	for range 60 {
		d.fits()
		if strings.Contains(d.view(), "…") {
			t.Fatalf("option text clipped:\n%s", d.view())
		}
		seen += " " + d.text()
		if !strings.Contains(d.view(), "↓ ") {
			break
		}
		d.keys("pgdown")
	}
	for i, o := range opts {
		if !strings.Contains(seen, string(rune('a'+i))+") "+o[:40]) || !strings.Contains(seen, o[len(o)-30:]) {
			t.Errorf("option %d never fully shown", i)
		}
	}
	if !strings.Contains(d.view(), "^u/^d scroll") {
		t.Error("footer does not offer scrolling")
	}
}

func TestLoadingUntilFirstRead(t *testing.T) {
	m := New(nil, &fakeAM{})
	m.Update(tea.WindowSizeMsg{Width: 92, Height: 29})
	if !strings.HasPrefix(m.View(), "loading…") {
		t.Fatalf("%q", m.View())
	}
}

func TestResizeReflows(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.longFixture()
	d.openWalk()
	for _, sz := range sizes {
		d.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		d.fits()
	}
}

func TestEveryScreenFits(t *testing.T) {
	for _, sz := range sizes {
		d := newDrv(t, sz[0], sz[1])
		d.longFixture()
		d.ask("570", "s1", "Second?", "x", "y")
		d.poll()
		d.fits()
		d.keys("enter")
		d.fits()
		d.keys("enter")
		for _, k := range []string{"v", "esc", "f", "esc", "?", "?", "a", "s"} {
			d.keys(k)
			d.fits()
		}
		if d.m.scr != scReview {
			t.Fatalf("%v: expected review, got %d", sz, d.m.scr)
		}
		d.keys("down", "enter")
		d.fits()
	}
}

func TestWalkReviewSendRecordsStaged(t *testing.T) {
	d := newDrv(t, 92, 29)
	for range 3 {
		d.ask("7", "s1", "Which way?", "left", "right")
	}
	d.openWalk()
	d.keys("b", "f")
	if d.m.scr != scFree {
		t.Fatal("f did not open free text")
	}
	d.keys("use the env var", "ctrl+d", "s")
	if d.m.scr != scReview {
		t.Fatalf("expected review, got %d", d.m.scr)
	}
	if !strings.Contains(d.view(), "7.3  Which way? → skipped") {
		t.Errorf("review:\n%s", d.view())
	}
	for _, id := range []string{"7.1", "7.2", "7.3"} {
		if d.get(id).Status != "open" {
			t.Fatalf("%s recorded before Send", id)
		}
	}
	d.keys("enter")
	if s := d.get("7.1"); s.AnswerOption != "b" {
		t.Errorf("7.1 = %+v", s)
	}
	if s := d.get("7.2"); s.AnswerText != "use the env var" {
		t.Errorf("7.2 = %+v", s)
	}
	if d.get("7.3").Status != "open" || len(d.am.sends) != 0 {
		t.Fatalf("7.3 should stay open and nothing sent; sends %v", d.am.sends)
	}
	if d.m.scr != scList || d.m.flash != "held until 7.3" {
		t.Fatalf("flash %q", d.m.flash)
	}
	d.poll()
	d.keys("enter", "enter", "a", "enter")
	if len(d.am.sends) != 1 || !strings.HasPrefix(d.m.flash, "sent 7.1, 7.2, 7.3 to spec-s1") {
		t.Fatalf("flash %q sends %v", d.m.flash, d.am.sends)
	}
	msg := d.am.sends[0][1]
	for _, want := range []string{"7.1 Which way? → b: right", "7.2 Which way? → free text: use the env var",
		"7.3 Which way? → a: left"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q missing from %q", want, msg)
		}
	}
}

func TestEmptyFreeTextReturnsToQuestion(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	at := time.Now()
	d.m.now = func() time.Time { return at }
	d.openWalk()
	before := d.view()
	d.keys("f", "ctrl+d")
	if d.m.scr != scWalk || d.view() != before {
		t.Fatalf("empty free text should return to the same question:\n%s", d.view())
	}
	d.keys("v")
	if d.m.scr != scFull || !strings.Contains(d.view(), "a) left") {
		t.Fatalf("view full:\n%s", d.view())
	}
	d.keys("esc", "a", "enter")
	if !strings.HasPrefix(d.m.flash, "sent 7.1") {
		t.Fatalf("flash %q", d.m.flash)
	}
}

func dump(d *drv) []inbox.Decision {
	d.t.Helper()
	ds, err := d.ib.Select("1")
	if err != nil {
		d.t.Fatal(err)
	}
	return ds
}

func TestCancelAndBackRecordNothing(t *testing.T) {
	for _, script := range [][]string{{"a", "s", "esc"}, {"a", "esc"}, {"a", "a", "down", "down", "enter"}} {
		d := newDrv(t, 92, 29)
		d.ask("7", "s1", "Which way?", "left", "right")
		d.ask("7", "s1", "Which way?", "left", "right")
		before := dump(d)
		d.openWalk()
		d.keys(script...)
		if !strings.Contains(d.m.flash, "nothing recorded") || d.m.scr != scList {
			t.Errorf("%v: flash %q", script, d.m.flash)
		}
		if !slices.EqualFunc(before, dump(d), func(a, b inbox.Decision) bool { return a.Rev == b.Rev }) || len(d.am.sends) > 0 {
			t.Errorf("%v changed the db", script)
		}
	}
}

func TestChangeOneRewalksOnlyThatQuestion(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "First?", "x", "y")
	d.ask("7", "s1", "Second?", "x", "y")
	d.openWalk()
	d.keys("a", "a", "down", "enter")
	if d.m.scr != scJump {
		t.Fatal("not on change")
	}
	d.keys("down", "enter")
	if d.m.p.cur != "7.2" || !strings.Contains(d.view(), "7.2  Second?") || strings.Contains(d.view(), "7.1  First?") {
		t.Fatalf("rewalk:\n%s", d.view())
	}
	d.keys("b")
	if d.m.scr != scReview {
		t.Fatal("change one should return to review")
	}
	d.keys("enter")
	if d.get("7.1").AnswerOption != "a" || d.get("7.2").AnswerOption != "b" || len(d.am.sends) != 1 {
		t.Fatalf("answers %v %v", d.get("7.1"), d.get("7.2"))
	}
}

func TestAnsweredElsewhereLeavesTheWalk(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.ask("7", "s1", "Which way?", "left", "right")
	d.openWalk()
	if _, err := d.other.Answer("7.1", ptr("b"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	d.poll()
	if d.m.p.cur != "7.2" || !strings.Contains(d.view(), "7.1 answered elsewhere: b: right") {
		t.Fatalf("cur %s\n%s", d.m.p.cur, d.view())
	}
	d.keys("a", "enter")
	if d.get("7.1").AnswerOption != "b" || d.get("7.2").AnswerOption != "a" {
		t.Fatal("answers")
	}
}

func ptr(s string) *string { return &s }

func TestAmendedShowsNewTextAndStaleStagedIsAskedAgain(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.ask("7", "s1", "Other?", "x", "y")
	d.openWalk()
	amend := func(q string, opts ...string) {
		if _, err := d.other.Amend("7.1", "s1", false, inbox.AmendInput{Question: &q, Options: opts}); err != nil {
			t.Fatal(err)
		}
		d.poll()
	}
	amend("Which road?", "up", "down")
	if !strings.Contains(d.view(), "7.1 (amended)  Which road?") || !strings.Contains(d.view(), "b) down") {
		t.Fatalf("amended text not shown:\n%s", d.view())
	}
	d.keys("a")
	amend("Which path?", "north", "south")
	if !strings.Contains(d.view(), "7.1 amended; asked again") {
		t.Fatalf("no stale marker:\n%s", d.view())
	}
	d.keys("x")
	d.keys("a")
	if d.m.p.cur != "7.1" || d.m.scr != scWalk {
		t.Fatalf("stale 7.1 not asked again before review: cur %q screen %d", d.m.p.cur, d.m.scr)
	}
	d.keys("b", "enter")
	if got := d.get("7.1"); got.AnswerOption != "b" || got.Question != "Which path?" {
		t.Fatalf("7.1 = %+v", got)
	}
}

func TestAmendAtSendIsShownAgain(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.openWalk()
	d.keys("a")
	q := "Which road?"
	if _, err := d.other.Amend("7.1", "s1", false, inbox.AmendInput{Question: &q, Options: []string{"up", "down"}}); err != nil {
		t.Fatal(err)
	}
	d.keys("enter")
	if d.m.scr != scWalk || !strings.Contains(d.view(), "7.1 amended; shown again") {
		t.Fatalf("backstop:\n%s", d.view())
	}
	d.keys("b", "enter")
	if !strings.Contains(d.am.sends[0][1], "7.1 Which road? → b: down") {
		t.Fatalf("sent %q", d.am.sends)
	}
}

func TestNewAskJoinsTheWalk(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.openWalk()
	d.ask("7", "s1", "Late?", "x", "y")
	d.poll()
	if !strings.Contains(d.view(), "7.2 asked; added to the walk") {
		t.Fatalf("no marker:\n%s", d.view())
	}
	d.keys("a")
	if !strings.Contains(d.view(), "7.2  Late?") {
		t.Fatalf("7.2 not walked:\n%s", d.view())
	}
	d.keys("b", "enter")
	if d.get("7.2").Status != "answered" || len(d.am.sends) != 1 {
		t.Fatal("not sent together")
	}
}

func TestNewAskDuringReviewGoesBackToWalk(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.openWalk()
	d.keys("a")
	d.ask("7", "s1", "Late?", "x", "y")
	d.poll()
	if d.m.scr != scWalk || d.m.p.cur != "7.2" {
		t.Fatalf("screen %d cur %q", d.m.scr, d.m.p.cur)
	}
}

func TestTalkResolvesOnlyChosen(t *testing.T) {
	d := newDrv(t, 92, 29)
	for range 3 {
		d.ask("7", "s1", "Which way?", "left", "right")
	}
	d.poll()
	d.keys("enter", "t")
	if d.m.scr != scTalkPick || !slices.Equal(d.am.popups, []string{"s1"}) {
		t.Fatalf("screen %d popups %v", d.m.scr, d.am.popups)
	}
	d.keys(" ", "down", "down", " ", "enter")
	if d.m.scr != scTalkNote {
		t.Fatal("no note prompt")
	}
	d.keys("talked", "enter")
	if d.m.flash != "resolved 7.1, 7.3" {
		t.Fatalf("flash %q", d.m.flash)
	}
	for id, want := range map[string]string{"7.1": "talk", "7.2": "", "7.3": "talk"} {
		if got := d.get(id); got.AnswerKind != want || (want != "" && got.Note != "talked") {
			t.Errorf("%s = %+v", id, got)
		}
	}
	d.poll()
	d.keys("enter", "t", "esc")
	if d.m.flash != "all left open" || d.get("7.2").Status != "open" || len(d.am.sends) != 0 {
		t.Fatalf("flash %q", d.m.flash)
	}
}

func TestTalkFromWalkResolvesCurrent(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "First?", "x", "y")
	d.ask("7", "s1", "Second?", "x", "y")
	d.openWalk()
	d.keys("t", " ", "enter", "esc")
	if d.m.scr != scWalk || d.m.p.cur != "7.2" || !strings.Contains(d.view(), "resolved 7.1") {
		t.Fatalf("screen %d cur %q\n%s", d.m.scr, d.m.p.cur, d.view())
	}
	d.keys("b", "enter")
	if len(d.am.sends) != 1 || strings.Contains(d.am.sends[0][1], "7.1") {
		t.Fatalf("sends %v", d.am.sends)
	}
}

func TestTalkUnavailablePrintsAttach(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.am.noPopup = true
	d.ask("7", "s1", "Which way?", "left", "right")
	d.poll()
	d.keys("enter", "t")
	if d.m.flash != "talk unavailable; attach with: tmux -L agentmgr attach -t am_s1" {
		t.Fatalf("flash %q", d.m.flash)
	}
}

func TestOneNoticePerNewItem(t *testing.T) {
	d := newDrvWith(t, 92, 29, func(d *drv) { d.ask("7", "s1", "Q?") })
	if len(d.am.notices) != 0 {
		t.Fatalf("first read should not notify: %v", d.am.notices)
	}
	title := "Brief title"
	if _, err := d.other.SetBrief("s1", "7", &title, nil); err != nil {
		t.Fatal(err)
	}
	d.ask("7", "s1", "First line?\nsecond line")
	d.ask("7", "s1", "Another?")
	d.ask("8", "s2", "Elsewhere?")
	if _, err := d.other.AddNote(inbox.NoteInput{Task: "7", Title: "FYI from chief", SessionID: "c1", Name: "chief"}); err != nil {
		t.Fatal(err)
	}
	d.poll()
	want := [][2]string{
		{"zen-inbox · #7 Brief title", "First line?"},
		{"zen-inbox · #7 Brief title", "Another?"},
		{"zen-inbox · #8", "Elsewhere?"},
		{"zen-inbox · #7 Brief title", "FYI from chief"},
	}
	if !slices.Equal(d.am.notices, want) {
		t.Fatalf("notices %q, want %q", d.am.notices, want)
	}
	d.poll()
	d.poll()
	if len(d.am.notices) != 4 {
		t.Fatalf("notified again on later polls: %q", d.am.notices)
	}
}

func TestListFollowsEveryChange(t *testing.T) {
	d := newDrv(t, 92, 29)
	last := d.view()
	changed := func(what string) {
		d.poll()
		if v := d.view(); v == last {
			t.Errorf("list did not change after %s:\n%s", what, v)
		} else {
			last = v
		}
	}
	d.ask("7", "s1", "Q?", "a", "b")
	changed("ask")
	d.ask("7", "s1", "Q?", "a", "b")
	d.ask("7", "s1", "Q?", "a", "b")
	changed("asks")
	if _, err := d.other.Withdraw("7.2", "s1", false); err != nil {
		t.Fatal(err)
	}
	changed("withdraw")
	if _, err := d.other.Answer("7.1", ptr("a"), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	changed("answer")
	if _, err := d.other.SetBrief("s1", "7", ptr("Zen board"), nil); err != nil {
		t.Fatal(err)
	}
	changed("brief")
	d.am.sessions = am.Sessions{am.Session{"id": "s1", "name": "spec-s1", "status": "busy", "running": true}}
	d.m.sessionsAt = time.Time{}
	changed("session status")
}

func TestListRetriesFailedDelivery(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Q?", "a", "b")
	if err := d.other.CloseRaw("7.1", "answered", "pick", "a", "", "held"); err != nil {
		t.Fatal(err)
	}
	d.poll()
	if !strings.Contains(d.view(), "retry spec-s1: 1 answer(s)") {
		t.Fatalf("no retry row:\n%s", d.view())
	}
	d.keys("enter")
	if d.m.flash != "sent 7.1 to s1" || len(d.am.sends) != 1 {
		t.Fatalf("flash %q", d.m.flash)
	}
}

func TestKeysAndFooterPerScreen(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "First?", "x", "y")
	d.ask("8", "s2", "Other?", "x", "y")
	d.poll()
	footer := func(want ...string) {
		t.Helper()
		v := d.view()
		for _, w := range want {
			if !strings.Contains(v, w) {
				t.Errorf("screen %d footer lacks %q:\n%s", d.m.scr, w, v)
			}
		}
	}
	footer("enter open", "q quit", "? help")
	d.keys("j")
	if d.m.cursor != 1 {
		t.Fatal("j did not move")
	}
	d.keys("k", "?")
	footer("One row per asking session")
	d.keys("?", "enter")
	if d.m.scr != scSession {
		t.Fatal("enter did not open the session")
	}
	footer("t talk", "esc back")
	d.keys("esc")
	if d.m.scr != scList {
		t.Fatal("esc did not go back")
	}
	d.keys("enter", "enter")
	footer("a-d pick", "f free", "s skip", "t talk", "v full", "esc back")
	d.keys("c")
	if d.m.scr != scWalk {
		t.Fatal("c picked a missing option")
	}
	d.keys("down", "down", "enter")
	if d.m.scr != scFree {
		t.Fatal("enter on Free text did not open it")
	}
	footer("ctrl+d send", "esc back")
	if strings.Contains(d.view(), "esc keep") {
		t.Fatal("blank box offers esc keep")
	}
	d.keys("x")
	footer("ctrl+d send", "esc keep")
	d.keys("backspace")
	d.keys("esc", "v")
	footer("esc back")
	d.keys("esc", "s")
	footer("esc cancel")
	d.keys("esc")
	if d.m.scr != scList {
		t.Fatal("esc on review did not cancel")
	}
	d.keys("q")
	if !d.quit {
		t.Fatal("q did not quit")
	}
}

func TestPollDuringSendKeepsThePass(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.openWalk()
	d.keys("a")
	_, cmd := d.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("send should batch the work with the spinner tick, got %T", batch)
	}
	sent := batch[0]()
	d.poll()
	d.send(sent)
	if d.m.scr != scList || !strings.HasPrefix(d.m.flash, "sent 7.1 to spec-s1") {
		t.Fatalf("flash %q", d.m.flash)
	}
	if strings.Contains(d.m.flash, "elsewhere") {
		t.Fatalf("own answer reported as elsewhere: %q", d.m.flash)
	}
}

func TestListCursorFollowsShrinkingList(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Q?", "a", "b")
	d.ask("8", "s2", "Q?", "a", "b")
	d.poll()
	d.keys("down")
	if _, err := d.other.Withdraw("8.1", "s2", false); err != nil {
		t.Fatal(err)
	}
	d.poll()
	if !strings.Contains(d.view(), "› spec-s1") {
		t.Fatalf("no row under the cursor:\n%s", d.view())
	}
}

var updatePanel = flag.Bool("update-panel", false, "rewrite testdata/panel goldens")

// golden compares got with testdata/panel/<name>.txt.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "panel", name+".txt")
	if *updatePanel {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got+"\n" != string(want) {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// startSend stages one answer per question and presses Send, leaving the
// send in flight; it returns the command that completes it.
func (d *drv) startSend() tea.Cmd {
	d.openWalk()
	d.keys("a")
	_, cmd := d.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return cmd
}

func TestBusyScreenGolden(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	for _, sz := range [][2]int{{60, 12}, {28, 8}} {
		d := newDrv(t, sz[0], sz[1])
		d.ask("7", "s1", "Which way?", "left", "right")
		d.ask("7", "s1", "And then?", "up", "down")
		d.ask("7", "s1", "Last?", "x", "y")
		d.poll()
		d.keys("enter", "enter", "a", "a", "a")
		if d.m.scr != scReview {
			t.Fatalf("not at review:\n%s", d.view())
		}
		d.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if d.m.busy == "" {
			t.Fatalf("no send in flight:\n%s", d.view())
		}
		v := d.view()
		for _, hidden := range []string{"Change one", "Cancel", "›", "enter", "esc", "q quit", "↑↓"} {
			if strings.Contains(v, hidden) {
				t.Errorf("busy screen shows %q:\n%s", hidden, v)
			}
		}
		d.fits()
		golden(t, "busy-sending-"+strconv.Itoa(sz[0])+"x"+strconv.Itoa(sz[1]), v)
	}
}

func TestBusyIgnoresKeysExceptCtrlC(t *testing.T) {
	d := newDrv(t, 60, 12)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.startSend()
	before := d.view()
	d.keys("a", "enter", "esc", "q", "?", "down")
	if d.view() != before || d.quit {
		t.Fatalf("keys reached a busy panel:\n%s", d.view())
	}
	d.keys("ctrl+c")
	if !d.quit {
		t.Fatal("ctrl+c did not quit while busy")
	}
}

func TestSpinnerTicksOnlyWhileBusy(t *testing.T) {
	d := newDrv(t, 60, 12)
	d.ask("7", "s1", "Which way?", "left", "right")
	if _, cmd := d.m.Update(tickMsg{}); cmd == nil {
		t.Fatal("poll should load")
	}
	if d.m.spinning {
		t.Fatal("spinner running while idle")
	}
	d.openWalk()
	d.keys("a")
	_, cmd := d.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || !d.m.spinning {
		t.Fatalf("send should start the spinner, got %T spinning=%v", cmd(), d.m.spinning)
	}
	first := d.view()
	_, next := d.m.Update(batch[1]())
	if next == nil || d.view() == first {
		t.Fatal("tick while busy should advance the frame and schedule the next")
	}
	d.send(batch[0]())
	if d.m.busy != "" {
		t.Fatal("send did not finish")
	}
	if _, next := d.m.Update(spinner.TickMsg{}); next != nil || d.m.spinning {
		t.Fatal("tick after the send should end the chain")
	}
}

func TestSentFlashShowsThenExpires(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 60, 12)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.run(d.startSend())
	if !d.m.flashOK || !strings.HasPrefix(d.view(), "» sent 7.1 to spec-s1") {
		t.Fatalf("flash %q ok=%v:\n%s", d.m.flash, d.m.flashOK, d.view())
	}
	golden(t, "sent-flash", d.view())
	now := d.m.now()
	d.m.now = func() time.Time { return now.Add(FlashFor + time.Second) }
	d.poll()
	if d.m.flash != "" || strings.Contains(d.view(), "sent 7.1") {
		t.Fatalf("flash did not expire:\n%s", d.view())
	}
}

func TestFailedSendKeepsErrorAndRetryHint(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 80, 12)
	d.ask("7", "s1", "Which way?", "left", "right")
	if err := d.other.CloseRaw("7.1", "answered", "pick", "a", "", "failed"); err != nil {
		t.Fatal(err)
	}
	d.poll()
	golden(t, "status-failed", d.view())
	if !strings.Contains(d.view(), "r retry") || !strings.Contains(d.view(), "zen-inbox retry s1") {
		t.Fatalf("failure hint gone:\n%s", d.view())
	}
	d.keys("r")
	if len(d.am.sends) != 1 {
		t.Fatalf("r did not retry: %v", d.am.sends)
	}
}

func TestStatusLineStates(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 60, 10)
	golden(t, "status-empty", d.view())
	d.ask("7", "s1", "Q?", "a", "b")
	d.ask("8", "s2", "Q?", "a", "b")
	d.ask("8", "s2", "Q2?", "a", "b")
	d.poll()
	golden(t, "status-open", d.view())
	last := func(v string) string {
		lines := strings.Split(v, "\n")
		return lines[len(lines)-1]
	}
	if got := last(d.view()); got != "◆ 3 open · spec-s1, spec-s2" {
		t.Errorf("open status %q", got)
	}
	d.send(tea.WindowSizeMsg{Width: 14, Height: 10})
	if got := last(d.view()); got != "◆ 3 open · sp…" {
		t.Errorf("status wrapped or overflowed: %q", got)
	}
}

// colour makes the panel render markdown with ANSI colours.
func (d *drv) colour() {
	lr := lipgloss.NewRenderer(io.Discard)
	lr.SetColorProfile(termenv.ANSI)
	d.m.md = md.New(lr)
}

var token = strings.Repeat("z", 120)

func (d *drv) codeFixture() {
	d.t.Helper()
	ctx := "Today it runs `zen-inbox watch` from chief.\n\n```go\nfunc main() {\n\tfmt.Println(\"hi\") // greet\n}\n```\n\n" +
		"```sh\nzen-inbox ask 7 -q 'Which?' | tee log\n```"
	_, err := d.other.Ask(inbox.AskInput{Task: "611", Question: "Keep `md.Lines` as the only renderer?",
		Options: []string{"yes, through `md.Lines` " + token, "no"}, Rec: "a", Context: ctx, SessionID: "s1",
		Name: "spec-s1", Title: "Glamour", Brief: "Render with **glamour**."})
	if err != nil {
		d.t.Fatal(err)
	}
}

// ansi16 fails on any 256 or true colour sequence.
func (d *drv) ansi16() {
	d.t.Helper()
	v := d.view()
	if strings.Contains(v, "38;5;") || strings.Contains(v, "38;2;") || strings.Contains(v, "48;5;") {
		d.t.Errorf("non ANSI-16 colour in\n%q", v)
	}
}

func TestPaintedScreensFit(t *testing.T) {
	for _, sz := range sizes {
		d := newDrv(t, sz[0], sz[1])
		d.colour()
		d.codeFixture()
		d.poll()
		check := func() { t.Helper(); d.fits(); d.ansi16() }
		check()
		d.keys("enter")
		check()
		if !strings.Contains(d.view(), "\x1b[36m") {
			t.Errorf("%v: session screen has no code colour:\n%s", sz, d.view())
		}
		d.keys("ctrl+d")
		check()
		d.keys("enter")
		for _, k := range []string{"?", "?", "down", "v", "ctrl+d", "down", "esc", "f", "esc", "a"} {
			d.keys(k)
			check()
		}
		if d.m.scr != scReview {
			t.Fatalf("%v: expected review, got %d", sz, d.m.scr)
		}
	}
}

func TestWalkShowsRenderedOptions(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.colour()
	d.codeFixture()
	d.openWalk()
	text := ansi.Strip(d.text())
	for _, want := range []string{"611.1  Keep md.Lines as the only renderer?", "a) yes, through md.Lines zzz", "b) no"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q missing from\n%s", want, ansi.Strip(d.view()))
		}
	}
	if strings.Contains(text, "`") {
		t.Errorf("backticks shown:\n%s", text)
	}
	if !strings.Contains(strings.ReplaceAll(text, " ", ""), token+"←rec") {
		t.Errorf("rec mark not after option a:\n%s", ansi.Strip(d.view()))
	}
}

func TestClipPaintedLineEndsReset(t *testing.T) {
	line := "\x1b[36m" + strings.Repeat("a", 30) + "\x1b[0m" + "\x1b[35m" + strings.Repeat("b", 30) + "\x1b[0m"
	got := clip(line, 40)
	if lipgloss.Width(got) > 40 || !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("%q is %d wide", got, lipgloss.Width(got))
	}
	if s := ansi.Strip(got); strings.ContainsAny(s, "\x1b[") {
		t.Fatalf("dangling escape in %q", got)
	}
}

func TestDefaultRendererPaintsNothingUnderTest(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.codeFixture()
	d.openWalk()
	for _, k := range []string{"", "v"} {
		if k != "" {
			d.keys(k)
		}
		if strings.Contains(d.view(), "\x1b") {
			t.Fatalf("escape bytes with the Ascii profile:\n%q", d.view())
		}
	}
}

const pinnedNow = "2026-11-10T12:00:00Z"

func TestLongContextScrollsWithCtrlD(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 30)
	ctx := make([]string, 40)
	for i := range ctx {
		ctx[i] = "context line " + strconv.Itoa(i+1)
	}
	if _, err := d.other.Ask(inbox.AskInput{Task: "669", Question: "Scroll?", Options: []string{"yes", "no"},
		Context: strings.Join(ctx, "\n"), SessionID: "s1", Name: "spec-s1"}); err != nil {
		t.Fatal(err)
	}
	d.poll()
	d.keys("enter")
	d.fits()
	if !strings.Contains(d.view(), "more") || strings.Contains(d.view(), "context line 40") {
		t.Fatalf("first page:\n%s", d.view())
	}
	golden(t, "scroll-session-top", d.view())
	d.keys("ctrl+d", "ctrl+d", "ctrl+d")
	d.fits()
	if !strings.Contains(d.view(), "context line 40") || !strings.Contains(d.view(), "↑ ") {
		t.Fatalf("ctrl+d did not reach the end:\n%s", d.view())
	}
	if !strings.Contains(d.view(), "› Answer") {
		t.Fatalf("menu not pinned:\n%s", d.view())
	}
	golden(t, "scroll-session-end", d.view())
	d.keys("ctrl+u", "ctrl+u", "ctrl+u")
	if strings.Contains(d.view(), "↑ ") {
		t.Fatalf("ctrl+u did not return to the top:\n%s", d.view())
	}
	d.send(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if d.m.scroll != 3 || !strings.Contains(d.view(), "↑ 3 more") {
		t.Fatalf("wheel scroll %d:\n%s", d.m.scroll, d.view())
	}
}

func TestSelectedOptionStaysVisible(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 30)
	d.ask("669", "s1", "Which way?", words(900), words(900), words(900))
	d.openWalk()
	if f, _ := d.m.walkFit(); !f.scroll {
		t.Fatal("expected the walk body to scroll")
	}
	golden(t, "scroll-walk-top", d.view())
	for _, k := range []string{"down", "ctrl+d", "down", "ctrl+d", "ctrl+d", "down", "ctrl+d", "down", "up"} {
		d.keys(k)
		d.fits()
		if !strings.Contains(d.view(), "› ") {
			t.Fatalf("after %s the selected option is gone:\n%s", k, d.view())
		}
	}
}

func (d *drv) manySessions(n int) {
	d.t.Helper()
	for i := range n {
		sid := "t" + strconv.Itoa(i+10)
		if _, err := d.other.Ask(inbox.AskInput{Task: strconv.Itoa(700 + i), Question: "Q?", Options: []string{"a", "b"},
			SessionID: sid, Name: "spec-" + sid}); err != nil {
			d.t.Fatal(err)
		}
	}
	d.poll()
}

func TestTallListScrollsWithTheCursor(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 30)
	d.manySessions(40)
	d.fits()
	if !strings.Contains(d.view(), "↓ ") {
		t.Fatalf("no more marker:\n%s", d.view())
	}
	golden(t, "scroll-list-top", d.view())
	for range 39 {
		d.keys("down")
	}
	d.fits()
	if !strings.Contains(d.view(), "› spec-t49") || !strings.Contains(d.view(), "↑ ") {
		t.Fatalf("cursor row not in view:\n%s", d.view())
	}
	golden(t, "scroll-list-end", d.view())
}

func TestTallHelpScrolls(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 30)
	d.manySessions(40)
	d.keys("?")
	if strings.Contains(d.view(), "One row per asking session") {
		t.Fatalf("help should be below the fold:\n%s", d.view())
	}
	for range 4 {
		d.keys("ctrl+d")
	}
	d.fits()
	if !strings.Contains(d.view(), "One row per asking session") {
		t.Fatalf("help not reached by scrolling:\n%s", d.view())
	}
	golden(t, "scroll-help-end", d.view())
}

func TestViewFullScrollsByLine(t *testing.T) {
	d := newDrv(t, 40, 15)
	d.longFixture()
	d.openWalk()
	d.keys("v")
	if !strings.Contains(d.view(), "↓ ") {
		t.Fatalf("view full does not mark more:\n%s", d.view())
	}
	d.keys("down")
	if d.m.scroll != 1 || !strings.Contains(d.view(), "↑ 1 more") {
		t.Fatalf("down scrolled to %d:\n%s", d.m.scroll, d.view())
	}
}

func TestWalkKeepsScrollAcrossViewFull(t *testing.T) {
	d := newDrv(t, 40, 15)
	d.ask("7", "s1", "Which way?", words(150), words(150), words(150))
	d.openWalk()
	d.keys("pgdown")
	before := d.m.scroll
	if before == 0 {
		t.Fatal("walk did not scroll")
	}
	d.keys("v", "down", "esc")
	if d.m.scroll != before {
		t.Fatalf("walk scroll %d, want %d", d.m.scroll, before)
	}
}

func TestEscOnFreeTextKeepsTypedText(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.ask("7", "s1", "And then?", "up", "down")
	d.openWalk()
	d.keys("f", "half typed", "esc")
	if d.m.scr != scWalk {
		t.Fatalf("esc did not return to the question:\n%s", d.view())
	}
	ds, err := d.ib.Drafts()
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 {
		t.Fatalf("drafts after esc: %+v", ds)
	}
	for _, dr := range ds {
		if dr.Kind != "text" || dr.Text != "half typed" {
			t.Fatalf("draft after esc: %+v", dr)
		}
	}
	d.keys("f")
	if d.m.scr != scFree || d.m.ta.Value() != "half typed" {
		t.Fatalf("free text reopened with %q", d.m.ta.Value())
	}
}

func TestEscOnBlankFreeTextStagesNothing(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.openWalk()
	d.keys("f", " ", "esc")
	if ds, err := d.ib.Drafts(); err != nil || len(ds) != 0 {
		t.Fatalf("drafts %+v, err %v", ds, err)
	}
}

func TestQuitWaitsForQueuedDraftWrites(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		d := newDrv(t, 92, 29)
		d.ask("7", "s1", "Which way?", "left", "right")
		d.ask("7", "s1", "And then?", "up", "down")
		d.openWalk()
		// A write in flight: the staged answer queues behind it.
		d.m.saving = true
		d.keys("a")
		if len(d.m.draftOps) != 1 {
			t.Fatalf("queued ops %+v", d.m.draftOps)
		}
		d.keys("esc")
		if k == "q" {
			d.keys("esc")
		}
		d.keys(k)
		if d.quit {
			t.Fatalf("%s quit with a draft unwritten", k)
		}
		d.m.saving = false
		d.send(draftsSavedMsg{})
		if !d.quit {
			t.Fatalf("%s did not quit once the writes landed", k)
		}
		if ds, err := d.ib.Drafts(); err != nil || len(ds) != 1 {
			t.Fatalf("%s: drafts %+v, err %v", k, ds, err)
		}
	}
}

func TestFailedDraftWriteCancelsQuit(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Which way?", "left", "right")
	d.openWalk()
	d.m.saving = true
	d.keys("esc", "q")
	d.m.saving = false
	d.send(draftsSavedMsg{err: errors.New("disk full")})
	if d.quit {
		t.Fatal("quit hid the failed draft write")
	}
}
