package am

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// fakes puts testdata/fakes first on PATH and returns the $FAKE_AM dir.
func fakes(t *testing.T, sessions string) string {
	t.Helper()
	bin, err := filepath.Abs("../../testdata/fakes")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "sends"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "sessions.json"), []byte(sessions), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_AM", data)
	return data
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sends(t *testing.T, data string) [][]string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(data, "sends"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out [][]string
	for _, n := range names {
		raw, _ := os.ReadFile(filepath.Join(data, "sends", n))
		parts := strings.Split(string(raw), "\x00")
		out = append(out, parts[:len(parts)-1])
	}
	return out
}

const live = `[{"id": "s1", "name": "spec-s1", "status": "idle", "running": true}]`

func TestSendArgvAndMessageID(t *testing.T) {
	data := fakes(t, live)
	ok, mid, errText := Exec{}.Send("s1", "hello")
	if !ok || mid != "42" || errText != "" {
		t.Fatal(ok, mid, errText)
	}
	want := []string{"send", "s1", "hello", "--from", "zen-inbox", "--json"}
	if got := sends(t, data); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatal(got)
	}
}

func TestGoneSessionFailsWithoutSending(t *testing.T) {
	data := fakes(t, `[{"id": "s1", "status": "dead", "running": false}]`)
	ok, _, errText := Exec{}.Send("s1", "hello")
	if ok || !strings.Contains(errText, "gone") || len(sends(t, data)) != 0 {
		t.Fatal(ok, errText)
	}
}

func TestSenderRefusedFallsBackWithoutFrom(t *testing.T) {
	data := fakes(t, live)
	touch(t, filepath.Join(data, "refuse_from"))
	if ok, _, errText := (Exec{}).Send("s1", "hello"); !ok {
		t.Fatal(errText)
	}
	got := sends(t, data)
	if len(got) != 2 || !slices.Contains(got[0], "--from") || slices.Contains(got[1], "--from") || got[0][2] != got[1][2] {
		t.Fatal(got)
	}
}

func TestOtherFailureKeepsFrom(t *testing.T) {
	data := fakes(t, live)
	touch(t, filepath.Join(data, "send_rc"))
	ok, _, errText := Exec{}.Send("s1", "hello")
	if ok || errText != "send refused" {
		t.Fatal(ok, errText)
	}
	if got := sends(t, data); len(got) != 1 || !slices.Contains(got[0], "--from") {
		t.Fatal(got)
	}
}

func TestSessionsUnavailableIsNil(t *testing.T) {
	data := fakes(t, live)
	if err := os.Remove(filepath.Join(data, "sessions.json")); err != nil {
		t.Fatal(err)
	}
	if s := (Exec{}).Sessions(); s != nil {
		t.Fatal(s)
	}
	if Status(nil) != "gone" || Status(Session{"running": true}) != "unknown" {
		t.Fatal("status")
	}
}

func TestPopupArgvAttachesOnManagerSocket(t *testing.T) {
	for _, env := range []string{"/tmp/tmux-501/agentmgr,123,4", "/tmp/tmux-501/default,9,0"} {
		argv := PopupArgv("ab12", env)
		want := []string{"tmux", "-S", strings.Split(env, ",")[0], "display-popup", "-E", "-w", "90%", "-h", "90%",
			"env -u TMUX tmux -L agentmgr attach -t am_ab12"}
		if !slices.Equal(argv, want) {
			t.Fatal(argv)
		}
	}
	if PopupArgv("ab12", "") != nil {
		t.Fatal("no $TMUX should mean no popup")
	}
	if got := AttachCommand("a b"); got != "tmux -L agentmgr attach -t 'am_a b'" {
		t.Fatal(got)
	}
}

func TestPopupRunsTmuxAndFailsWithoutServer(t *testing.T) {
	data := fakes(t, live)
	if !(Exec{TmuxEnv: "/tmp/sock,1,2"}).Popup("ab12") {
		t.Fatal("popup failed")
	}
	raw, _ := os.ReadFile(filepath.Join(data, "tmux.argv"))
	argv := strings.Split(string(raw), "\x00")
	if !slices.Equal(argv[:3], []string{"-S", "/tmp/sock", "display-popup"}) || !strings.HasSuffix(argv[len(argv)-2], "attach -t am_ab12") {
		t.Fatal(argv)
	}
	if (Exec{}).Popup("ab12") {
		t.Fatal("popup without $TMUX")
	}
	touch(t, filepath.Join(data, "tmux_rc"))
	if (Exec{TmuxEnv: "/tmp/sock,1,2"}).Popup("ab12") {
		t.Fatal("failed tmux reported success")
	}
}

func TestDirName(t *testing.T) {
	for in, want := range map[string]string{"/x/dotfiles": "dotfiles", "/x/dotfiles/": "dotfiles", "": "", "/": "", "a/.": "a"} {
		if got := DirName(in); got != want {
			t.Errorf("%q → %q", in, got)
		}
	}
}
