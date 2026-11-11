package am

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func notified(t *testing.T, data string) [][]string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(data, "notified"))
	var out [][]string
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(data, "notified", e.Name()))
		parts := strings.Split(string(raw), "\x00")
		out = append(out, parts[:len(parts)-1])
	}
	return out
}

func TestNotifyDarwinPostsOnceWithSoundAndActivate(t *testing.T) {
	data := fakes(t, "[]")
	t.Setenv("__CFBundleIdentifier", "org.alacritty")
	notify("darwin", "zen-inbox · #7 Title", "Which way?")
	got := notified(t, data)
	want := []string{"terminal-notifier", "-title", "zen-inbox · #7 Title",
		"-message", "Which way?", "-sound", Sound, "-activate", "org.alacritty"}
	if len(got) != 1 || strings.Join(got[0], "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestNotifyDarwinFallsBackToOsascriptWhenDenied(t *testing.T) {
	data := fakes(t, "[]")
	t.Setenv("__CFBundleIdentifier", "")
	if err := os.WriteFile(filepath.Join(data, "notifier_rc"), []byte("3"), 0o644); err != nil {
		t.Fatal(err)
	}
	notify("darwin", "T", "body")
	got := notified(t, data)
	if len(got) != 2 || got[0][0] != "terminal-notifier" || got[1][0] != "osascript" {
		t.Fatalf("got %q", got)
	}
	if args := got[1]; args[len(args)-2] != "T" || args[len(args)-1] != "body" {
		t.Fatalf("osascript args %q", args)
	}
}

func TestNotifyEscapesLeadingBracket(t *testing.T) {
	data := fakes(t, "[]")
	notify("darwin", "T", "[x] y")
	if got := notified(t, data); len(got) != 1 || got[0][4] != `\[x] y` {
		t.Fatalf("got %q", got)
	}
}

func TestNotifyLinuxUsesZenBell(t *testing.T) {
	data := fakes(t, "[]")
	notify("linux", "T", "body")
	raw, _ := os.ReadFile(filepath.Join(data, "bells"))
	if got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"); strings.Join(got, "|") != "window-attention|-n|T|-b|body" {
		t.Fatalf("got %q", got)
	}
	if got := notified(t, data); len(got) != 0 {
		t.Fatalf("macOS notifier ran on linux: %q", got)
	}
}
