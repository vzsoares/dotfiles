package am

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// withZenChief puts the real scripts/chief.py on PATH as zen-chief; only
// agent-manager is stubbed.
func withZenChief(t *testing.T, sessions string) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("python3 is required: zen-chief is a python script")
	}
	data := fakes(t, sessions)
	script, err := filepath.Abs("../../../scripts/chief.py")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(script, filepath.Join(bin, "zen-chief")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return data
}

const chiefs = `[
 {"id": "old", "name": "chief", "tool": "claude", "status": "idle", "running": true, "archived": true},
 {"id": "c1", "name": "chief", "tool": "claude", "status": "idle", "running": true},
 {"id": "s1", "name": "worker", "tool": "claude", "status": "idle", "running": true}]`

func TestChiefSendsToTheLiveChiefWithoutTouchingOthers(t *testing.T) {
	data := withZenChief(t, chiefs)
	id, errText := (Exec{}).Chief("-dash\nsecond line")
	if id != "42" || errText != "" {
		t.Fatalf("id %q err %q", id, errText)
	}
	got := sends(t, data)
	want := []string{"send", "c1", "-dash\nsecond line", "--json", "--from", "zenha"}
	if len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("sends %q", got)
	}
}

func TestChiefErrorsAreSpecificAndSendNothing(t *testing.T) {
	cases := map[string]struct{ sessions, want string }{
		"none": {`[{"id": "s1", "name": "worker", "status": "idle", "running": true}]`,
			"no live session named 'chief'"},
		"two": {`[{"id": "a", "name": "chief", "status": "idle", "running": true},
			{"id": "b", "name": "chief", "status": "idle", "running": true}]`,
			"2 live sessions named 'chief': a, b"},
	}
	for name, c := range cases {
		data := withZenChief(t, c.sessions)
		id, errText := (Exec{}).Chief("hi")
		if id != "" || errText != c.want {
			t.Errorf("%s: id %q err %q", name, id, errText)
		}
		if got := sends(t, data); len(got) != 0 {
			t.Errorf("%s: sent %q", name, got)
		}
	}
}

func TestChiefStatusReportsAgentManagersState(t *testing.T) {
	data := withZenChief(t, chiefs)
	if got, errText := (Exec{}).ChiefStatus("42"); got != "queued" || errText != "" {
		t.Fatalf("%q %q", got, errText)
	}
	if err := os.WriteFile(filepath.Join(data, "msg_status"), []byte("delivered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := (Exec{}).ChiefStatus("42"); got != "delivered" {
		t.Fatal(got)
	}
}

func TestMessageStatusAsksAgentManagerDirectly(t *testing.T) {
	data := fakes(t, chiefs)
	if got, errText := (Exec{}).MessageStatus("42"); got != "queued" || errText != "" {
		t.Fatalf("%q %q", got, errText)
	}
	if err := os.WriteFile(filepath.Join(data, "msg_status"), []byte("dropped"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := (Exec{}).MessageStatus("42"); got != "dropped" {
		t.Fatal(got)
	}
}
