package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// zenBin is the binary under test, built once by TestMain.
var zenBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "zen-inbox-test")
	if err != nil {
		panic(err)
	}
	zenBin = filepath.Join(dir, "zen-inbox")
	build := exec.Command("go", "build", "-o", zenBin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fake is a temp root with the fake agent-manager, tmux and zen-bell on PATH
// and a temp db.
type fake struct {
	t    *testing.T
	root string
	data string
	db   string
	env  []string
}

func session(id, status string, running bool) map[string]any {
	return map[string]any{"id": id, "name": "spec-" + id, "status": status,
		"running": running, "archived": false}
}

func newFake(t *testing.T) *fake {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	f := &fake{t: t, root: root, data: filepath.Join(root, "am"), db: filepath.Join(root, "zen-flow.db")}
	bin := filepath.Join(root, "bin")
	for _, d := range []string{bin, filepath.Join(f.data, "sends")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src, _ := filepath.Abs("testdata/fakes")
	for _, name := range []string{"agent-manager", "tmux", "zen-bell"} {
		body, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, name), body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.sessions(session("s1", "idle", true), session("s2", "idle", true))
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "PATH", "AGENT_MANAGER_SESSION_ID", "TMUX", "ZEN_FLOW_DB", "FAKE_AM", "COLUMNS", "ZEN_INBOX_NOW":
			continue
		}
		f.env = append(f.env, kv)
	}
	f.env = append(f.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_AM="+f.data, "ZEN_FLOW_DB="+f.db)
	return f
}

func (f *fake) sessions(ss ...map[string]any) {
	f.t.Helper()
	raw, _ := json.Marshal(ss)
	f.write("sessions.json", string(raw))
}

func (f *fake) write(name, body string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.data, name), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fake) remove(name string) {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.data, name)); err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
}

// sends is every recorded `agent-manager send` argv, oldest first.
func (f *fake) sends() [][]string {
	f.t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.data, "sends"))
	if err != nil {
		f.t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := make([][]string, 0, len(names))
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(f.data, "sends", n))
		if err != nil {
			f.t.Fatal(err)
		}
		parts := strings.Split(string(raw), "\x00")
		out = append(out, parts[:len(parts)-1])
	}
	return out
}

type result struct {
	code           int
	stdout, stderr string
}

// cmd builds a zen-inbox (or other) process in the fake environment.
func (f *fake) cmd(extra []string, argv ...string) *exec.Cmd {
	c := exec.Command(argv[0], argv[1:]...)
	c.Env = append(append([]string(nil), f.env...), extra...)
	return c
}

func run(c *exec.Cmd) result {
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return result{code: -1, stderr: err.Error()}
		}
		code = exit.ExitCode()
	}
	return result{code, out.String(), errb.String()}
}

// zen runs the binary under test with extra env entries.
func (f *fake) zen(extra []string, args ...string) result {
	return run(f.cmd(extra, append([]string{zenBin}, args...)...))
}

func (f *fake) sqlite(sql string) string {
	f.t.Helper()
	out, err := exec.Command("sqlite3", f.db, sql).CombinedOutput()
	if err != nil {
		f.t.Fatalf("sqlite3 %q: %v\n%s", sql, err, out)
	}
	return string(out)
}
