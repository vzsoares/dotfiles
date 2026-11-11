package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

func (d *drv) addNote(task, title, body string) inbox.Note {
	d.t.Helper()
	n, err := d.other.AddNote(inbox.NoteInput{Task: task, Title: title, Body: body, SessionID: "c1",
		Name: inbox.NoteSender})
	if err != nil {
		d.t.Fatal(err)
	}
	return n
}

func (d *drv) noteRow(id int64) inbox.Note {
	d.t.Helper()
	n, err := d.ib.Note(id)
	if err != nil {
		d.t.Fatal(err)
	}
	return n
}

// notesFixture is an open decision, an unread note and a read one.
func (d *drv) notesFixture() {
	d.ask("7", "s1", "Which way?", "left", "right")
	d.addNote("707", "Task 707 merged", "Tag v1.2.3; follow-up #708.")
	d.addNote("", "Session spec-s2 died after a revive", "")
	if err := d.other.MarkNotes([]int64{2}, true); err != nil {
		d.t.Fatal(err)
	}
	d.poll()
}

// cursorTo moves the list cursor onto the row whose label holds text.
func (d *drv) cursorTo(text string) {
	d.t.Helper()
	for i, it := range d.m.listItems() {
		if strings.Contains(it.label, text) {
			d.m.cursor = i
			return
		}
	}
	d.t.Fatalf("no row %q:\n%s", text, d.view())
}

func (d *drv) last() string {
	lines := strings.Split(d.view(), "\n")
	return lines[len(lines)-1]
}

// line is the view's row i.
func (d *drv) line(i int) string { return strings.Split(d.view(), "\n")[i] }

// head is the header's status, without the quota on its right.
func (d *drv) head() string {
	return strings.TrimRight(strings.TrimSuffix(d.line(0), "quota n/a"), " ")
}

func TestNotesListGoldens(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	for _, sz := range [][2]int{{92, 30}, {60, 20}, {40, 15}} {
		d := newDrv(t, sz[0], sz[1])
		d.notesFixture()
		v := d.view()
		if !strings.Contains(v, "Notes · 1 unread") || !strings.Contains(v, "● Task 707 merged") ||
			!strings.Contains(v, "○ Session spec-s2") {
			t.Fatalf("no notes section:\n%s", v)
		}
		d.fits()
		golden(t, "notes-list-"+strconv.Itoa(sz[0])+"x"+strconv.Itoa(sz[1]), v)
		if d.head() != pyfmtClip("cockpit · 1 open · ✉ 1 unread", sz[0]) {
			t.Errorf("header %q", d.head())
		}
		if !strings.HasPrefix(d.last(), "enter open · a answer") && !strings.HasPrefix(d.last(), "↑↓ · enter open · a answer") {
			t.Errorf("footer %q", d.last())
		}
	}
}

func pyfmtClip(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func TestNotesOnlyStatusAndCursorAcrossRows(t *testing.T) {
	d := newDrv(t, 60, 12)
	if d.head() != "cockpit · all clear" {
		t.Fatal(d.head())
	}
	d.addNote("", "one", "")
	d.poll()
	if d.head() != "cockpit · ✉ 1 unread" {
		t.Fatalf("%q", d.head())
	}
	if d.last() != "enter open · m read · f reply · c chief · ? help · q quit" {
		t.Fatalf("footer %q", d.last())
	}
	d.ask("7", "s1", "Q?", "a", "b")
	d.poll()
	d.keys("down")
	if n, ok := d.m.cursorNote(); !ok || n.Title != "one" {
		t.Fatalf("cursor did not reach the note row:\n%s", d.view())
	}
	if !strings.Contains(d.view(), "› ● one") {
		t.Fatalf("%s", d.view())
	}
}

func TestNoteScreenScrollsLongMarkdown(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	var body []string
	for i := range 30 {
		body = append(body, "## Part "+strconv.Itoa(i)+"\n\n"+words(100))
	}
	md := strings.Join(body, "\n\n")
	if len(md) < 3000 {
		t.Fatalf("body is %d bytes", len(md))
	}
	d := newDrv(t, 60, 20)
	d.addNote("707", "Long report", md)
	d.poll()
	d.keys("enter")
	if d.m.scr != scNote {
		t.Fatalf("not on the note:\n%s", d.view())
	}
	if !d.noteRow(1).Unread() {
		t.Fatal("opening the note marked it read")
	}
	d.fits()
	golden(t, "note-top", d.view())
	if !strings.HasPrefix(d.line(1), "n1 · #707 · chief · 0s") {
		t.Fatalf("%s", d.view())
	}
	d.keys("j")
	if d.m.scroll != 1 {
		t.Fatalf("j scrolled to %d", d.m.scroll)
	}
	for range 40 {
		d.keys("pgdown")
	}
	d.fits()
	golden(t, "note-end", d.view())
	if !strings.Contains(d.text(), "Part 29") {
		t.Fatalf("end not reached:\n%s", d.view())
	}
	d.send(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	before := d.m.scroll
	d.keys("ctrl+u")
	if d.m.scroll >= before {
		t.Fatal("ctrl+u did not scroll up")
	}
	d.keys("esc")
	if d.m.scr != scList {
		t.Fatal("esc did not go back")
	}
}

func TestMarkKeysChangeTheDB(t *testing.T) {
	d := newDrv(t, 60, 20)
	d.notesFixture()
	d.cursorTo("Task 707")
	d.keys("m")
	if d.noteRow(1).Unread() {
		t.Fatal("m on the list did not mark read")
	}
	d.cursorTo("Task 707")
	d.keys("m")
	if !d.noteRow(1).Unread() {
		t.Fatal("m again did not mark unread")
	}
	d.keys("M")
	if d.noteRow(1).Unread() || d.noteRow(2).Unread() {
		t.Fatal("M did not mark all read")
	}
	d.cursorTo("Task 707")
	d.keys("enter", "m")
	if !d.noteRow(1).Unread() || d.m.scr != scNote {
		t.Fatal("m on the note screen did not toggle")
	}
	for _, k := range []string{"left", "right", "shift+tab", "l"} {
		d.keys(k)
		if d.m.scr != scNote {
			t.Fatalf("%s left the note screen", k)
		}
	}
}

func TestReplySendsOnceAndMarksRead(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 60, 20)
	d.addNote("707", "Task 707 merged", "body")
	d.poll()
	d.keys("enter", "f")
	if d.m.scr != scReply {
		t.Fatalf("f did not open the reply box:\n%s", d.view())
	}
	d.keys("ctrl+d")
	if d.m.scr != scNote || len(d.am.sends) != 0 {
		t.Fatal("an empty reply sent or stayed")
	}
	d.keys("f")
	d.keys("thanks, ship it")
	d.fits()
	golden(t, "note-reply", d.view())
	_, cmd := d.m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if d.m.busy == "" {
		t.Fatal("no send in flight")
	}
	golden(t, "note-sending", d.view())
	d.run(cmd)
	want := "[zen-inbox] Reply to your note n1 (#707 Task 707 merged):\nthanks, ship it"
	if len(d.am.sends) != 1 || d.am.sends[0] != [2]string{"c1", want} {
		t.Fatalf("%q", d.am.sends)
	}
	if d.m.scr != scList || !d.m.flashOK || !strings.HasPrefix(d.line(1), "» reply sent to chief") {
		t.Fatalf("%s", d.view())
	}
	golden(t, "note-sent-flash", d.view())
	if n := d.noteRow(1); n.Unread() || n.Delivery != "sent" {
		t.Fatalf("%+v", n)
	}
	if !strings.Contains(d.view(), "○ Task 707 merged · chief · #707 · 0s ↩") {
		t.Fatalf("no reply mark:\n%s", d.view())
	}
	d.cursorTo("Task 707")
	d.keys("enter", "f")
	if d.m.scr != scNote || !strings.Contains(d.view(), "already replied") || len(d.am.sends) != 1 {
		t.Fatalf("f on a replied note:\n%s", d.view())
	}
	if !strings.Contains(d.text(), "↩ replied: thanks, ship it") {
		t.Fatalf("%s", d.view())
	}
}

func TestFailedReplyRetryRowAndStatus(t *testing.T) {
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	d := newDrv(t, 60, 20)
	d.addNote("707", "Task 707 merged", "")
	if err := d.other.Exec("UPDATE notes SET reply = 'thanks', delivery = 'failed', delivery_error = 'gone',"+
		" replied_at = ? WHERE id = 1", inbox.Stamp(inbox.Now())); err != nil {
		t.Fatal(err)
	}
	d.poll()
	if !strings.Contains(d.view(), "retry reply n1") || d.head() != "cockpit · ✕ delivery failed · chief · r retry" {
		t.Fatalf("%s", d.view())
	}
	d.fits()
	golden(t, "note-failed", d.view())
	d.keys("r")
	if len(d.am.sends) != 1 || d.am.sends[0][0] != "c1" {
		t.Fatalf("r did not retry: %v", d.am.sends)
	}
	if strings.Contains(d.view(), "retry reply") {
		t.Fatalf("%s", d.view())
	}
}

func TestNoteScreensFit(t *testing.T) {
	for _, sz := range [][2]int{{92, 30}, {60, 20}, {40, 15}} {
		d := newDrv(t, sz[0], sz[1])
		d.notesFixture()
		d.addNote("707", strings.Repeat("t", inbox.TitleMax), "# Report\n\n"+words(3000))
		d.poll()
		d.fits()
		d.cursorTo("ttt")
		d.keys("enter")
		d.fits()
		d.keys("?")
		d.fits()
		d.keys("?", "f")
		d.keys(words(2000))
		d.fits()
		d.run(func() tea.Msg { _, cmd := d.m.Update(tea.KeyMsg{Type: tea.KeyCtrlD}); return cmd() })
		d.fits()
		d.cursorTo("ttt")
		d.keys("enter")
		for range 60 {
			d.keys("pgdown")
		}
		d.fits()
	}
}

func TestNoteNeverRingsAndShowsOnNextPoll(t *testing.T) {
	d := newDrv(t, 60, 20)
	d.addNote("", "background", "")
	if strings.Contains(d.view(), "background") {
		t.Fatal("shown before a poll")
	}
	d.poll()
	if !strings.Contains(d.view(), "background") {
		t.Fatalf("not shown after the poll:\n%s", d.view())
	}
	if len(d.am.bells) != 0 {
		t.Fatalf("a note rang: %v", d.am.bells)
	}
}

func TestFailedReplyKeepsCursorOnItsNote(t *testing.T) {
	d := newDrv(t, 60, 20)
	d.addNote("", "older", "")
	d.addNote("", "newer", "")
	d.poll()
	d.cursorTo("older")
	d.keys("enter", "f", "hi")
	d.am.fail = "session gone"
	d.keys("ctrl+d")
	if !strings.Contains(d.view(), "retry reply n1") {
		t.Fatalf("%s", d.view())
	}
	if n, ok := d.m.cursorNote(); !ok || n.ID != 1 {
		t.Fatalf("cursor moved off the replied note:\n%s", d.view())
	}
}

func TestRefusedReplyKeepsText(t *testing.T) {
	d := newDrv(t, 60, 20)
	d.addNote("", "raced", "")
	d.poll()
	d.keys("enter", "f", "my words")
	if _, err := d.other.Reply(1, "other panel"); err != nil {
		t.Fatal(err)
	}
	d.keys("ctrl+d")
	if d.m.scr != scNote || !strings.Contains(d.view(), "already replied") {
		t.Fatalf("%s", d.view())
	}
	if d.m.ta.Value() != "my words" {
		t.Fatalf("text lost: %q", d.m.ta.Value())
	}
}
