package tui

import (
	"flag"
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

	"github.com/vzsoares/dotfiles/inbox/internal/am"
	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
	"github.com/vzsoares/dotfiles/inbox/internal/view"
)

type fakeAM struct {
	mu       sync.Mutex
	sessions am.Sessions
	sends    [][2]string
	popups   []string
	bells    []string
	noPopup  bool
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
	"pgup": tea.KeyPgUp,
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
		f, _, _ := d.m.walkFit()
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
		f := fitWalk(dec, b, nil, 92, h, len(questionMenu(dec)), 1, 0)
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
	f, _, _ := d.m.walkFit()
	if !f.scroll {
		t.Fatalf("expected a scrolling body, cuts %v", f.cuts)
	}
	seen := ""
	for range 10 {
		d.fits()
		if strings.Contains(d.view(), "…") {
			t.Fatalf("option text clipped:\n%s", d.view())
		}
		seen += " " + d.text()
		d.keys("pgdown")
	}
	for i, o := range opts {
		if !strings.Contains(seen, string(rune('a'+i))+") "+o[:40]) || !strings.Contains(seen, o[len(o)-30:]) {
			t.Errorf("option %d never fully shown", i)
		}
	}
	if !strings.Contains(d.view(), "pgup/dn") {
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
	if d.m.scr != scChange {
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

func TestOneBellPerSessionPerCycle(t *testing.T) {
	d := newDrvWith(t, 92, 29, func(d *drv) { d.ask("7", "s1", "Q?") })
	if len(d.am.bells) != 0 {
		t.Fatalf("first read should not ring: %v", d.am.bells)
	}
	for range 3 {
		d.ask("7", "s1", "Q?")
	}
	d.ask("8", "s2", "Q?")
	d.poll()
	got := append([]string(nil), d.am.bells...)
	slices.Sort(got)
	if !slices.Equal(got, []string{"spec-s1: 3 new decision(s)", "spec-s2: 1 new decision(s)"}) {
		t.Fatalf("bells %v", got)
	}
	d.poll()
	if len(d.am.bells) != 2 {
		t.Fatalf("rang again: %v", d.am.bells)
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
	footer("ctrl+d send", "esc cancel")
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
