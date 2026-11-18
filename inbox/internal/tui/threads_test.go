package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

// fixture843 is #843's real shape: ten notes, nine with one reply, and the
// open decision 843.1 asked between them; n3 and n10 are unread.
func (d *drv) fixture843() {
	d.t.Helper()
	t0 := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	at := func(m int) { d.t.Setenv("ZEN_INBOX_NOW", t0.Add(time.Duration(m)*time.Minute).Format(time.RFC3339)) }
	for i := 1; i <= 10; i++ {
		at(i * 5)
		if i == 5 {
			if _, err := d.other.Ask(inbox.AskInput{Task: "843", Question: "Which **phone** app?", Options: []string{"Blink", "Termius"},
				SessionID: "s1", Name: "spec-s1", Title: "Phone cockpit setup", Brief: "Reach the cockpit from the phone over SSH."}); err != nil {
				d.t.Fatal(err)
			}
		}
		d.addNote("843", "Phone step "+strconv.Itoa(i), "Set up **step "+strconv.Itoa(i)+"**.")
	}
	for i := 1; i <= 9; i++ {
		at(60 + i)
		if _, err := d.other.Reply(int64(i), "ok "+strconv.Itoa(i)); err != nil {
			d.t.Fatal(err)
		}
	}
	if err := d.other.MarkNotes([]int64{3}, false); err != nil {
		d.t.Fatal(err)
	}
	d.t.Setenv("ZEN_INBOX_NOW", pinnedNow)
}

// threadCursor is the focused timeline entry's label.
func (d *drv) threadCursor() string {
	it, ok := d.m.threadItem()
	switch {
	case !ok:
		return ""
	case it.Note != nil:
		return it.Note.Label()
	case it.Decision != nil:
		return it.Decision.ID
	}
	return it.Chat.Label()
}

func (d *drv) openThread843() {
	d.t.Helper()
	d.poll()
	d.cursorTo("#843")
	d.keys("o")
	if d.m.scr != scThread || !d.m.thread.loaded {
		d.t.Fatalf("thread not open:\n%s", d.view())
	}
}

func (d *drv) has(want ...string) {
	d.t.Helper()
	v := d.view()
	for _, w := range want {
		if !strings.Contains(v, w) {
			d.t.Errorf("view lacks %q:\n%s", w, v)
		}
	}
}

func TestThreadGoldens(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.ask("9", "s2", "Ship it?", "a", "b")
	d.addNote("", "Swept 6 finished agents; kept 5", "")
	d.poll()
	d.fits()
	d.has("Threads · 2", "› ▸ #843 · Phone cockpit setup · 1 open · 2 unread · 11 items · 1h", "  spec-s2 · #9",
		"No task · 1 unread", "enter expand")
	if strings.Contains(d.view(), "Phone step") {
		t.Fatalf("thread not collapsed:\n%s", d.view())
	}
	golden(t, "cockpit-threads-collapsed-92x29", d.view())

	d.keys("z")
	d.fits()
	d.has("› ▾ #843", "    spec-s1 · #843", "    ● Phone step 10 · chief · 1h", "    ○ Phone step 9 · chief · 1h ↩")
	if strings.Contains(d.view(), "· #843 · 1h") {
		t.Fatalf("child note repeats the task:\n%s", d.view())
	}
	golden(t, "cockpit-threads-expanded-92x29", d.view())

	d.keys("z", "o")
	d.fits()
	if d.threadCursor() != "n3" {
		t.Fatalf("opened at %s, want the first unread n3:\n%s", d.threadCursor(), d.view())
	}
	d.has("#843 Phone cockpit setup", "1 open · 2 unread · 11 entries", "› n3 · chief")
	golden(t, "thread-843-unread-92x29", d.view())
	if d.noteRow(3).Unread() == false {
		t.Fatal("opening the thread marked n3 read")
	}
	d.keys("g")
	d.fits()
	d.has("Reach the cockpit", "n1 · chief", "↩ 59m: ok 1")
	golden(t, "thread-843-top-92x29", d.view())
	d.keys("G")
	d.fits()
	d.has("Phone step 10", "esc back")
	golden(t, "thread-843-end-92x29", d.view())
}

func TestThreadNoTaskAndRefileGoldens(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.ask("861", "s1", "Pick one?", "a", "b")
	d.addNote("", "Good night", "")
	d.addNote("", "Swept 6 finished agents", "")
	d.addNote("", "06h report", "")
	if err := d.other.MarkNotes([]int64{1}, true); err != nil {
		t.Fatal(err)
	}
	d.poll()
	d.fits()
	d.has("Threads · 1", "› spec-s1 · #861", "No task · 2 unread", "○ Good night")
	golden(t, "cockpit-threads-notask-92x29", d.view())
	d.cursorTo("Swept")
	d.keys("T")
	if d.m.scr != scRefile {
		t.Fatalf("T did not open the refile box:\n%s", d.view())
	}
	d.fits()
	d.has("refile n2  Swept 6 finished agents", "enter save")
	golden(t, "refile-input-92x29", d.view())
}

func TestThreadOpenAskGolden(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 92, 29)
	d.addNote("861", "Spec drafted", "Read it in **Notion**.")
	if err := d.other.MarkNotes([]int64{1}, true); err != nil {
		t.Fatal(err)
	}
	d.ask("861", "s1", "Re-file with T?", "yes", "no")
	d.poll()
	d.cursorTo("#861")
	d.keys("o")
	if d.threadCursor() != "861.1" {
		t.Fatalf("opened at %q, want the open ask", d.threadCursor())
	}
	d.fits()
	d.has("861.1 · spec-s1 · 0s", "Re-file with T?", "open", "n1 · chief")
	golden(t, "thread-open-ask-92x29", d.view())
}

func TestThreadsGroupingOrderAndSingletons(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.addNote("5", "five a", "")
	d.addNote("5", "five b", "")
	d.ask("7", "s1", "Seven?", "a", "b")
	// s1 asks for 8 later: the session stays under its oldest task, 7.
	d.ask("8", "s1", "Eight?", "a", "b")
	d.addNote("8", "eight note", "")
	d.askProd("s2")
	d.addNote("680", "prod note", "")
	d.poll()
	var got []string
	for _, it := range d.m.listItems() {
		got = append(got, it.value)
	}
	want := []string{"i:", "t:680", "s:s1", "t:5", "n:3"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v:\n%s", got, want, d.view())
	}
	d.has("PROD", "spec-s1 · #7, #8", "● eight note · chief · #8")
	if th, _ := d.m.threadOf("s:s1"); th.open != 1 {
		t.Fatalf("#7 counts %d open, want only its own ask", th.open)
	}
}

func TestThreadFoldKeysAndPersist(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.poll()
	d.cursorTo("#843")
	d.keys("right")
	if !d.m.threadsOpen["843"] || d.m.focusedRow() != "t:843" {
		t.Fatalf("→ did not expand:\n%s", d.view())
	}
	d.keys("right")
	if !d.m.threadsOpen["843"] {
		t.Fatal("→ toggled instead of expanding")
	}
	d.cursorTo("Phone step 4")
	d.keys("left")
	if d.m.threadsOpen["843"] || d.m.focusedRow() != "t:843" {
		t.Fatalf("← on a child did not collapse onto the header:\n%s", d.view())
	}
	d.keys("z")
	again := newDrvWith(t, 92, 29, func(n *drv) {
		n.m.stateFile = d.m.stateFile
		n.fixture843()
	})
	again.poll()
	if !again.m.threadsOpen["843"] || !strings.Contains(again.view(), "▾ #843") {
		t.Fatalf("expanded state not restored:\n%s", again.view())
	}
	again.cursorTo("#843")
	again.keys("left")
	third := newDrvWith(t, 92, 29, func(n *drv) {
		n.m.stateFile = d.m.stateFile
		n.fixture843()
	})
	third.poll()
	if strings.Contains(third.view(), "▾ #843") {
		t.Fatalf("collapse not saved:\n%s", third.view())
	}
	if st := readState(d.m.stateFile); len(st.ThreadsOpen) != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestThreadCursorSurvivesPolls(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.addNote("900", "lone", "")
	d.poll()
	d.cursorTo("#843")
	// A prod ask sorts above #843.
	d.askProd("s2")
	d.poll()
	if d.m.focusedRow() != "t:843" {
		t.Fatalf("cursor left the thread row: %q\n%s", d.m.focusedRow(), d.view())
	}
	d.keys("z")
	d.cursorTo("Phone step 4")
	d.addNote("843", "Phone step 11", "")
	d.poll()
	if d.m.focusedRow() != "n:4" {
		t.Fatalf("cursor left the child: %q\n%s", d.m.focusedRow(), d.view())
	}
	d.cursorTo("lone")
	d.addNote("900", "second", "")
	d.poll()
	if d.m.focusedRow() != "t:900" {
		t.Fatalf("a singleton that became a thread lost the cursor: %q\n%s", d.m.focusedRow(), d.view())
	}
}

func TestThreadChildRowsKeepTheirKeys(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.poll()
	d.cursorTo("#843")
	d.keys("z")
	d.cursorTo("Phone step 10")
	d.keys("enter")
	if d.m.scr != scNote || d.m.note.ID != 10 {
		t.Fatalf("enter on a child note:\n%s", d.view())
	}
	d.keys("esc")
	if d.m.scr != scList || d.m.focusedRow() != "n:10" {
		t.Fatalf("esc from the note: %q\n%s", d.m.focusedRow(), d.view())
	}
	d.keys("m")
	if d.noteRow(10).Unread() {
		t.Fatal("m on a child did not mark it read")
	}
	d.keys("f")
	if d.m.scr != scReply || d.m.note.ID != 10 {
		t.Fatalf("f on a child:\n%s", d.view())
	}
	d.keys("esc", "esc")
	d.cursorTo("spec-s1")
	d.keys("a")
	if d.m.scr != scWalk || d.m.p.cur != "843.1" {
		t.Fatalf("a on a child:\n%s", d.view())
	}
}

func TestThreadHeaderKeys(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.addNote("", "loose", "")
	d.poll()
	d.cursorTo("#843")
	d.keys("m")
	for _, id := range []int64{3, 10} {
		if d.noteRow(id).Unread() {
			t.Fatalf("n%d still unread", id)
		}
	}
	if !d.noteRow(11).Unread() {
		t.Fatal("m marked a note outside the thread")
	}
	d.cursorTo("#843")
	d.keys("a")
	if d.m.scr != scWalk || d.m.p.cur != "843.1" {
		t.Fatalf("a on the header:\n%s", d.view())
	}
}

func TestThreadTabEnterAndEscChains(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.openThread843()
	for d.threadCursor() != "843.1" {
		before := d.threadCursor()
		d.keys("tab")
		if d.threadCursor() == before {
			t.Fatalf("tab stuck on %s", before)
		}
	}
	d.keys("enter")
	if d.m.scr != scWalk || d.m.p.cur != "843.1" {
		t.Fatalf("enter on the open ask:\n%s", d.view())
	}
	d.keys("esc")
	if d.m.scr != scJump {
		t.Fatalf("esc from the walk: screen %d", d.m.scr)
	}
	d.keys("esc")
	if d.m.scr != scThread || d.threadCursor() != "843.1" {
		t.Fatalf("esc from the decision list: screen %d at %s\n%s", d.m.scr, d.threadCursor(), d.view())
	}
	d.keys("shift+tab", "enter")
	if d.m.scr != scNote || d.m.note.ID != 4 {
		t.Fatalf("enter on n4:\n%s", d.view())
	}
	d.keys("esc")
	if d.m.scr != scThread || d.threadCursor() != "n4" {
		t.Fatalf("esc from the note: screen %d at %s", d.m.scr, d.threadCursor())
	}
	d.keys("esc")
	if d.m.scr != scList || d.m.focusedRow() != "t:843" {
		t.Fatalf("esc from the thread: %q\n%s", d.m.focusedRow(), d.view())
	}
}

func TestThreadReplyTargets(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.openThread843()
	d.keys("tab")
	if d.threadCursor() != "n4" {
		t.Fatalf("at %s", d.threadCursor())
	}
	d.keys("f")
	if d.m.scr != scReply || d.m.note.ID != 4 {
		t.Fatalf("f on n4: %+v", d.m.note)
	}
	d.keys("esc", "esc")
	if d.m.scr != scThread {
		t.Fatalf("esc chain from the reply box: screen %d", d.m.scr)
	}
	d.keys("tab")
	if d.threadCursor() != "843.1" {
		t.Fatalf("at %s", d.threadCursor())
	}
	d.keys("f", "thanks")
	if d.m.scr != scReply || d.m.note.ID != 10 {
		t.Fatalf("f off a note should reply to the newest: %+v", d.m.note)
	}
	d.keys("ctrl+d")
	if d.m.scr != scThread || !strings.Contains(d.view(), "reply sent to chief") {
		t.Fatalf("reply did not return to the thread:\n%s", d.view())
	}
	if n := d.noteRow(10); len(n.Replies) != 1 || n.Replies[0].Text != "thanks" {
		t.Fatalf("%+v", n.Replies)
	}
}

func TestThreadMarkReadsOnlyItsNotes(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.addNote("", "loose", "")
	d.openThread843()
	d.keys("m")
	if d.noteRow(3).Unread() || d.noteRow(10).Unread() || !d.noteRow(11).Unread() {
		t.Fatal("m in the thread marked the wrong notes")
	}
	d.has("11 entries")
	if strings.Contains(d.line(1), "unread") {
		t.Fatalf("counts not refreshed: %q", d.line(1))
	}
}

func TestRefileIntoAThreadAndBack(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.addNote("", "Phone notes before the task", "")
	d.poll()
	d.cursorTo("before the task")
	d.keys("T", "843", "enter")
	if n := d.noteRow(11); n.Task != "843" {
		t.Fatalf("%+v", n)
	}
	if d.m.scr != scList || d.m.focusedRow() != "t:843" || !strings.Contains(d.view(), "» n11 → #843") {
		t.Fatalf("list did not follow the note: %q\n%s", d.m.focusedRow(), d.view())
	}
	d.keys("o", "G")
	for d.threadCursor() != "n11" {
		d.keys("tab")
	}
	d.keys("T", "ctrl+u", "-", "enter")
	if n := d.noteRow(11); n.Task != "" {
		t.Fatalf("%+v", n)
	}
	if d.m.scr != scThread || strings.Contains(d.view(), "before the task") {
		t.Fatalf("thread still shows the note:\n%s", d.view())
	}
	d.keys("esc")
	d.has("No task · 1 unread", "● Phone notes before the task")
	d.cursorTo("before the task")
	d.keys("T", "ctrl+u", "x1", "enter")
	if d.m.scr != scRefile || !strings.Contains(d.view(), "bad task") {
		t.Fatalf("bad task not refused:\n%s", d.view())
	}
}

func TestChatFromThreadIsBoundStoredAndShown(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.openThread843()
	d.keys("c")
	if d.m.scr != scChief || !strings.Contains(d.view(), "[#843 Phone cockpit setup]") {
		t.Fatalf("compose not bound:\n%s", d.view())
	}
	d.keys("status?", "ctrl+d")
	if len(d.am.chiefs) != 1 || d.am.chiefs[0] != "[#843 Phone cockpit setup] status?" {
		t.Fatalf("%q", d.am.chiefs)
	}
	c, err := d.ib.Chat(1)
	if err != nil || c.Task != "843" || c.Text != "status?" || c.MessageID != "42" || c.Status != "queued" {
		t.Fatalf("%+v %v", c, err)
	}
	if d.m.scr != scThread {
		t.Fatalf("send did not return to the thread: screen %d", d.m.scr)
	}
	d.am.chiefStatus = "delivered"
	d.poll()
	if c, _ := d.ib.Chat(1); c.Status != "delivered" {
		t.Fatalf("status not mirrored: %+v", c)
	}
	d.poll()
	d.keys("G")
	d.fits()
	d.has("✉ you → chief · 0s · delivered", "status?", "12 entries")
	golden(t, "thread-chat-92x29", d.view())

	d.keys("esc")
	d.addNote("", "loose", "")
	d.poll()
	d.cursorTo("loose")
	d.keys("c", "hello", "ctrl+d")
	if got := d.am.chiefs[len(d.am.chiefs)-1]; got != "hello" {
		t.Fatalf("%q", got)
	}
	if c, _ := d.ib.Chat(2); c.Task != "" || c.Text != "hello" {
		t.Fatalf("%+v", c)
	}
}

func TestChatSendFailureIsStored(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.openThread843()
	d.am.chiefErr = "chief is gone"
	d.keys("c", "hi", "ctrl+d")
	if d.m.scr != scChief || !strings.Contains(d.view(), "chief is gone") {
		t.Fatalf("%s", d.view())
	}
	if c, _ := d.ib.Chat(1); c.Status != "failed" || c.Error != "chief is gone" || c.Task != "843" {
		t.Fatalf("%+v", c)
	}
	d.keys("esc")
	if d.m.scr != scThread {
		t.Fatalf("esc from compose: screen %d", d.m.scr)
	}
}

func TestPhoneThreadGoldens(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, phoneW, phoneH)
	d.fixture843()
	d.addNote("", "Swept 6 finished agents", "")
	d.poll()
	d.fits()
	golden(t, "phone-threads-44x24", d.view())
	d.openThread843()
	d.fits()
	golden(t, "phone-thread-44x24", d.view())
}

func TestThreadScreensFitSmallPanes(t *testing.T) {
	for _, sz := range [][2]int{{60, 20}, {40, 15}} {
		d := newDrv(t, sz[0], sz[1])
		d.fixture843()
		d.addNote("", "loose", "")
		d.poll()
		d.fits()
		d.cursorTo("#843")
		d.keys("z")
		d.fits()
		d.keys("z", "o")
		for _, k := range []string{"g", "tab", "tab", "pgdown", "?", "G", "?", "c"} {
			d.keys(k)
			d.fits()
		}
		d.keys("esc", "T")
		d.fits()
	}
}

func TestWalkFromThreadEndsOnTheThread(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"send", []string{"a", "a", "enter"}},
		{"cancel", []string{"a", "a", "esc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDrv(t, 92, 29)
			d.fixture843()
			d.openThread843()
			d.keys(tc.keys...)
			if d.m.scr != scThread || d.m.thread.task != "843" {
				t.Fatalf("screen %d:\n%s", d.m.scr, d.view())
			}
		})
	}
}

func TestPollPrunesThreadsOpenOfGoneThreads(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.m.threadsOpen["843"], d.m.threadsOpen["999"] = true, true
	d.poll()
	if d.m.threadsOpen["999"] || !d.m.threadsOpen["843"] {
		t.Fatalf("threadsOpen = %v", d.m.threadsOpen)
	}
	if st := readState(d.m.stateFile); len(st.ThreadsOpen) != 1 || st.ThreadsOpen[0] != "843" {
		t.Fatalf("saved %v", st.ThreadsOpen)
	}
}

func TestEnterTogglesThreadRowAndOpensItemRow(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.fixture843()
	d.poll()
	d.cursorTo("#843")
	d.keys("enter")
	if d.m.scr != scList || !d.m.threadsOpen["843"] || d.m.focusedRow() != "t:843" {
		t.Fatalf("enter did not expand:\n%s", d.view())
	}
	d.keys("enter")
	if d.m.scr != scList || d.m.threadsOpen["843"] {
		t.Fatalf("enter did not collapse:\n%s", d.view())
	}
	d.keys("enter")
	d.cursorTo("Phone step 10")
	d.keys("enter")
	if d.m.scr != scNote || d.m.note.ID != 10 {
		t.Fatalf("enter on an item row:\n%s", d.view())
	}
}

func (d *drv) inboxFixture() {
	d.ask("7", "s1", "Q one?", "a", "b")
	d.ask("8", "s2", "Q two?", "a", "b")
	d.addNote("707", "Unread merged", "")
	read := d.addNote("", "Already read", "")
	if err := d.ib.MarkNotes([]int64{read.ID}, true); err != nil {
		d.t.Fatal(err)
	}
}

func TestInboxRowIsFirstSelectedAndCountsRefresh(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.inboxFixture()
	d.pollKeep()
	d.fits()
	if d.m.focusedRow() != "i:" || d.m.cursor != 0 {
		t.Fatalf("Inbox row not selected first: %q\n%s", d.m.focusedRow(), d.view())
	}
	d.has("› Inbox · 2 decisions · 1 note")
	d.addNote("", "Another unread", "")
	d.pollKeep()
	d.fits()
	d.has("› Inbox · 2 decisions · 2 notes")
	if strings.Contains(d.head(), "decision") {
		t.Fatalf("header repeats the counts: %q", d.head())
	}
}

func TestInboxEnterListsOnlyUnreadNotesAndOpenDecisions(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.inboxFixture()
	d.pollKeep()
	d.rawKeys("enter")
	var got []string
	for _, it := range d.m.listItems() {
		got = append(got, it.value)
	}
	want := []string{"s:s1", "s:s2", "n:1"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v:\n%s", got, want, d.view())
	}
	d.has("spec-s1 · #7 · Q one?", "Unread merged")
	if strings.Contains(d.view(), "Already read") {
		t.Fatalf("read note listed:\n%s", d.view())
	}
	d.rawKeys("esc")
	if d.m.inboxOpen || d.m.focusedRow() != "i:" {
		t.Fatalf("esc did not leave the Inbox list:\n%s", d.view())
	}
}

func TestInboxFinishingAnItemOpensTheNextOrReturnsToTheInbox(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.inboxFixture()
	d.pollKeep()
	d.rawKeys("enter", "enter", "enter", "a", "enter")
	if d.get("7.1").Status != "answered" {
		t.Fatalf("7.1 not answered:\n%s", d.view())
	}
	if d.m.scr != scSession || d.m.sid != "s2" {
		t.Fatalf("next decision did not open: screen %d sid %q\n%s", d.m.scr, d.m.sid, d.view())
	}
	d.rawKeys("enter", "a", "enter")
	if d.m.scr != scNote || d.m.note.Title != "Unread merged" {
		t.Fatalf("next unread note did not open:\n%s", d.view())
	}
	d.rawKeys("enter")
	if d.m.scr != scList || d.m.inboxOpen {
		t.Fatalf("last item should end on the cockpit once the Inbox is empty:\n%s", d.view())
	}
}

func TestInboxLastItemReturnsToTheInboxList(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.addNote("", "First unread", "")
	d.addNote("", "Second unread", "")
	d.pollKeep()
	d.rawKeys("enter", "down", "enter")
	if d.m.scr != scNote {
		t.Fatalf("note not open:\n%s", d.view())
	}
	d.rawKeys("enter")
	if d.m.scr != scList || !d.m.inboxOpen || len(d.m.listItems()) != 1 {
		t.Fatalf("did not return to the Inbox list:\n%s", d.view())
	}
}

func (d *drv) threadOf900() {
	d.ask("900", "s1", "Q one?", "a", "b")
	d.ask("900", "s2", "Q two?", "a", "b")
	d.addNote("900", "Unread merged", "")
	d.poll()
	d.cursorTo("#900")
	d.keys("o")
	if d.m.scr != scThread || !d.m.thread.loaded {
		d.t.Fatalf("thread not open:\n%s", d.view())
	}
}

func TestThreadFinishingADecisionOpensTheNextUnreadItem(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.threadOf900()
	items := d.m.thread.data.Items
	if len(items) != 3 || items[0].Decision == nil || items[1].Decision == nil || items[2].Note == nil {
		t.Fatalf("thread items %+v", items)
	}
	d.m.cursor = 0
	d.keys("enter", "a", "enter")
	if d.get("900.1").Status != "answered" || d.m.scr != scWalk || d.m.p.cur != "900.2" {
		t.Fatalf("next open decision did not open: screen %d\n%s", d.m.scr, d.view())
	}
	d.keys("a", "enter")
	if d.m.scr != scNote || d.m.noteFrom != scThread {
		t.Fatalf("next unread note did not open:\n%s", d.view())
	}
	d.keys("enter")
	if d.m.scr != scThread {
		t.Fatalf("last item did not return to the thread:\n%s", d.view())
	}
}

func TestThreadFinishingADecisionReturnsWhenNextIsRead(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("901", "s1", "Q one?", "a", "b")
	read := d.addNote("901", "Old news", "")
	if err := d.ib.MarkNotes([]int64{read.ID}, true); err != nil {
		t.Fatal(err)
	}
	d.poll()
	d.cursorTo("#901")
	d.keys("o")
	d.m.cursor = 0
	d.keys("enter", "a", "enter")
	if d.get("901.1").Status != "answered" || d.m.scr != scThread {
		t.Fatalf("did not return to the thread: screen %d\n%s", d.m.scr, d.view())
	}
}

func TestListFinishingADecisionOpensTheNextUnansweredRow(t *testing.T) {
	d := newDrv(t, 92, 29)
	d.ask("7", "s1", "Q one?", "a", "b")
	d.ask("8", "s2", "Q two?", "a", "b")
	d.poll()
	d.cursorTo("spec-s1")
	d.keys("a", "a", "enter")
	if d.get("7.1").Status != "answered" || d.m.scr != scSession || d.m.sid != "s2" {
		t.Fatalf("next unanswered row did not open: screen %d\n%s", d.m.scr, d.view())
	}
	d.keys("enter", "a", "enter")
	if d.m.scr != scList {
		t.Fatalf("no row left, want the list: screen %d\n%s", d.m.scr, d.view())
	}
}
