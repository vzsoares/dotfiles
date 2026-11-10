package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The golden files were captured from inbox.py through pyshim.py; the Go
// binary must reproduce them byte for byte.
var goldenPython = flag.String("golden-python", "",
	"capture goldens with this command (e.g. \"uv run -q --script testdata/golden/pyshim.py\")")

const goldenNow = "2026-01-01T12:00:00+00:00"

type gstep struct {
	name  string
	args  []string
	env   []string
	files map[string]string // written into $FAKE_AM before the step
	rm    []string          // removed from $FAKE_AM before the step
	sql   string            // run through sqlite3 before the step
	home  map[string]string // written under $ROOT before the step
	// usage marks a usage error: only the exit code is compared, since
	// Click's and cobra's own error text is not part of the parity surface.
	usage bool
}

func s1(args ...string) gstep { return gstep{args: args, env: []string{"AGENT_MANAGER_SESSION_ID=s1"}} }

func named(name string, s gstep) gstep { s.name = name; return s }

func plain(args ...string) gstep { return gstep{args: args} }

func usage(args ...string) gstep { return gstep{args: args, usage: true} }

func goldenScenario() []gstep {
	sessions := `[{"id": "s1", "name": "spec-s1", "status": "idle", "running": true, "archived": false,` +
		` "group": "me", "directory": "/x/dotfiles"}, {"id": "s2", "name": "spec-s2", "status": "busy",` +
		` "running": true, "archived": false}, {"id": "s3", "name": "spec-s3", "status": "dead",` +
		` "running": false, "archived": false}`
	for n := 4; n <= 11; n++ {
		sessions += fmt.Sprintf(`, {"id": "s%d", "name": "spec-s%d", "status": "idle", "running": true}`, n, n)
	}
	sessions += "]"
	long := strings.Repeat("lorem ipsum dolor sit amet ", 12)
	steps := []gstep{
		{name: "setup", args: []string{"list"}, files: map[string]string{"sessions.json": sessions}},
		named("no-args", usage()),
		named("unknown-command", usage("nope")),
		named("unknown-flag", usage("list", "--bogus")),
		named("ask-no-session", plain("ask", "7", "-q", "Q?")),
		named("ask-missing-q", usage("ask", "7", "--session", "s1")),
		named("ask-missing-task", usage("ask", "-q", "Q?", "--session", "s1")),
		named("ask-one-option", plain("ask", "7", "-q", "Q?", "-o", "one", "--session", "s1")),
		named("ask-five-options", plain("ask", "7", "-q", "Q?", "-o", "a", "-o", "b", "-o", "c",
			"-o", "d", "-o", "e", "--session", "s1")),
		named("ask-blank-option", plain("ask", "7", "-q", "Q?", "-o", "a1", "-o", "  ", "--session", "s1")),
		named("ask-bad-rec", plain("ask", "7", "-q", "Q?", "-o", "a1", "-o", "b1", "--rec", "c", "--session", "s1")),
		named("ask-rec-no-options", plain("ask", "7", "-q", "Q?", "--rec", "a", "--session", "s1")),
		named("ask-bad-task", plain("ask", "bad task", "-q", "Q?", "--session", "s1")),
		named("ask-bad-task-quote", plain("ask", "it's", "-q", "Q?", "--session", "s1")),
		named("ask-blank-question", plain("ask", "7", "-q", "   ", "--session", "s1")),
		named("ask-first", plain("ask", "7", "-q", "Which way?", "-o", "left", "-o", "right",
			"--rec", "b", "--session", "s1")),
		named("ask-env-interspersed", s1("ask", "-q", "Free?", "7")),
		named("ask-gate-json-brief", plain("ask", "7", "--gate", "-q", "Ship?", "-o", "yes", "-o", "no",
			"--session", "s1", "--json", "--title", "Zen board", "--brief", "line one\nline two")),
		named("ask-gate-2", plain("ask", "7", "--gate", "-q", "Again?", "--session", "s1")),
		named("ask-unicode-json", plain("ask", "8", "-q", "Unicode é 😀 \"quoted\" \\ tab\t?",
			"-o", "é", "--option=日本", "--context", "ctx\nline2", "--session", "s2", "--json")),
		named("ask-unknown-session", plain("ask", "9", "-q", "Who?", "--session", "zz", "-oa", "-ob")),
		named("ask-rec-substring", plain("ask", "10", "--rec", "ab", "-q", "Q?", "-o", "x", "-o", "y",
			"-o", "z", "--session", "s2")),
		named("ask-empty-session-flag", s1("ask", "7", "-q", "Late?", "--session", "")),
		named("brief-nothing", plain("brief", "7", "--session", "s1")),
		named("brief-text", plain("brief", "7", "--session", "s1", "--text", "New brief")),
		named("brief-title-json", plain("brief", "7", "--session", "s1", "--title", "T", "--json")),
		named("brief-bad-task", plain("brief", "x y", "--title", "t", "--session", "s1")),
		named("brief-no-session", plain("brief", "7", "--title", "x")),
		named("brief-empty-title", s1("brief", "8", "--title", "")),
		named("show", plain("show", "7.1")),
		named("show-json", plain("show", "7.1", "--json")),
		named("show-missing", plain("show", "7.99")),
		named("show-extra-arg", usage("show", "7.1", "extra")),
		named("amend-nothing", s1("amend", "7.1")),
		named("amend-blank-q", s1("amend", "7.1", "-q", "")),
		named("amend-not-owner", gstep{args: []string{"amend", "7.1", "-q", "New?"},
			env: []string{"AGENT_MANAGER_SESSION_ID=s2"}}),
		named("amend-force", gstep{args: []string{"amend", "7.1", "-q", "Which road?", "-o", "up",
			"-o", "down", "-o", "sideways", "--force"}, env: []string{"AGENT_MANAGER_SESSION_ID=s2"}}),
		named("amend-one-option", s1("amend", "7.1", "-o", "only")),
		named("amend-bad-rec", s1("amend", "7.1", "-o", "x", "-o", "y", "--rec", "c")),
		named("amend-keep-rec", s1("amend", "7.1", "-o", "up", "-o", "down")),
		named("amend-drop-rec", s1("amend", "7.2", "-o", "p", "-o", "q")),
		named("amend-context", s1("amend", "7.1", "--context", "new ctx")),
		named("amend-missing", s1("amend", "7.99", "-q", "x?")),
		named("list-json", plain("list", "--json")),
		named("list-all", plain("list", "--all")),
		named("list-80", plain("list")),
		named("list-40", gstep{args: []string{"list"}, env: []string{"COLUMNS=40"}}),
		named("answer-neither", plain("answer", "7.1")),
		named("answer-both", plain("answer", "7.1", "a", "--text", "x")),
		named("answer-blank-text", plain("answer", "7.1", "--text", "  ")),
		named("answer-no-option", plain("answer", "7.1", "z")),
		named("answer-empty-letter", plain("answer", "7.1", "")),
		named("answer-missing", plain("answer", "7.99", "a")),
		named("answer-held", plain("answer", "7.1", "b", "--note", "ship it")),
		named("answer-again", plain("answer", "7.1", "a")),
		named("resolve-closed", plain("resolve", "7.1")),
		named("amend-closed", s1("amend", "7.1", "-q", "x?", "--force")),
		named("withdraw-closed", s1("withdraw", "7.1", "--force")),
		named("withdraw-not-owner", gstep{args: []string{"withdraw", "7.2"},
			env: []string{"AGENT_MANAGER_SESSION_ID=s2"}}),
		named("withdraw-held", s1("withdraw", "7.2")),
		named("answer-text-json", plain("answer", "7.g", "--text", "use the env var", "--json")),
		named("list-held", plain("list")),
		named("answer-late", plain("answer", "7.3", "a")),
		named("resolve-flushes", plain("resolve", "7.g2", "--note", "talked")),
		named("show-sent", plain("show", "7.1")),
		named("show-talk", plain("show", "7.g2")),
		named("show-withdrawn", plain("show", "7.2")),
		named("show-text", plain("show", "7.g", "--json")),
		{name: "fail-ask", args: []string{"ask", "11", "-q", "Fails?", "-o", "a", "-o", "b", "--session", "s4"},
			files: map[string]string{"send_rc": "1"}},
		named("fail-answer", plain("answer", "11.1", "a")),
		named("fail-list", plain("list")),
		named("fail-show", plain("show", "11.1")),
		named("retry-unknown", plain("retry", "nope")),
		named("retry-by-name-fails", plain("retry", "spec-s4")),
		{name: "retry-ok", args: []string{"retry", "s4"}, rm: []string{"send_rc"}},
		named("retry-again", plain("retry", "s4")),
		named("refuse-ask", plain("ask", "12", "-q", "From?", "-o", "a", "-o", "b", "--session", "s5")),
		{name: "refuse-answer", args: []string{"answer", "12.1", "b"}, files: map[string]string{"refuse_from": ""}},
		{name: "gone-ask", args: []string{"ask", "13", "-q", "Gone?", "--session", "s3"}, rm: []string{"refuse_from"}},
		named("gone-answer", plain("answer", "13.1", "--text", "hi")),
		named("gone-answer-json", plain("show", "13.1", "--json")),
	}
	outs := []struct{ name, body string }{
		{"refused", `{"refused": "too many"}`},
		{"error-true", `{"error": true}`},
		{"id-only", `{"id": 7}`},
		{"not-json", `not json`},
		{"null-id", `{"message_id": null}`},
		{"list-out", `[1]`},
		{"empty-out", ``},
	}
	for i, o := range outs {
		task := fmt.Sprint(14 + i)
		steps = append(steps,
			named("out-ask-"+o.name, plain("ask", task, "-q", "Out?", "--session", "s6")),
			gstep{name: "out-answer-" + o.name, args: []string{"answer", task + ".1", "--text", "ok"},
				files: map[string]string{"send_out": o.body}},
			named("out-show-"+o.name, plain("show", task+".1")))
	}
	steps = append(steps,
		gstep{name: "stale-ask", args: []string{"ask", "30", "-q", "Stale?", "--session", "s7"},
			rm: []string{"send_out"}},
		gstep{name: "stale-in-flight", args: []string{"retry", "s7"},
			sql: "UPDATE decisions SET status = 'answered', answer_kind = 'text', answer_text = 'x'," +
				" delivery = 'sending', updated_at = '2026-01-01T11:59:30+00:00' WHERE id = '30.1'"},
		gstep{name: "stale-list", args: []string{"list"}},
		gstep{name: "stale-retry", args: []string{"retry", "s7"},
			sql: "UPDATE decisions SET updated_at = '2026-01-01T11:58:00+00:00' WHERE id = '30.1'"},
		named("held-ask", plain("ask", "31", "-q", "Held?", "-o", "a", "-o", "b", "--session", "s7")),
		gstep{name: "held-list", args: []string{"list"},
			sql: "UPDATE decisions SET status = 'answered', answer_kind = 'pick', answer_option = 'a'," +
				" delivery = 'held' WHERE id = '31.1'"},
		named("held-retry", plain("retry", "spec-s7")),
		named("last-ask-1", gstep{args: []string{"ask", "32", "-q", "One?", "-o", "a", "-o", "b"}, env: []string{"AGENT_MANAGER_SESSION_ID=s8"}}),
		named("last-ask-2", gstep{args: []string{"ask", "32", "-q", "Two?"}, env: []string{"AGENT_MANAGER_SESSION_ID=s8"}}),
		named("last-answer", plain("answer", "32.1", "a")),
		named("last-withdraw-flushes", s1("withdraw", "32.2", "--force")),
		named("force-ask", plain("ask", "33", "-q", "Force?", "--session", "s9")),
		named("force-withdraw", s1("withdraw", "33.1", "--force")),
		named("talk-alone-ask", plain("ask", "34", "-q", "Talk?", "--session", "s9")),
		named("talk-alone-resolve", plain("resolve", "34.1")),
		named("big-ask-1", plain("ask", "35", "-q", strings.Repeat("Q", 3000), "-o", long, "-o", "b", "--session", "s10")),
		named("big-ask-2", plain("ask", "35", "-q", strings.Repeat("R", 3000), "-o", "a", "-o", "b", "--session", "s10")),
		named("big-ask-3", plain("ask", "35", "-q", strings.Repeat("S", 3000), "--session", "s10")),
		named("big-answer-1", plain("answer", "35.1", "a", "--note", long)),
		named("big-answer-2", plain("answer", "35.2", "b")),
		named("big-answer-3", plain("answer", "35.3", "--text", strings.Repeat("é", 9000))),
	)
	for n := 1; n <= 10; n++ {
		steps = append(steps, named(fmt.Sprintf("order-ask-%d", n), plain("ask", "36", "-q", "Order?", "--session", "s11")))
	}
	steps = append(steps,
		named("order-held-10", plain("answer", "36.10", "--text", "ten")),
		named("order-held-2", plain("answer", "36.2", "--text", "two")),
		named("order-list", plain("list")),
		gstep{name: "ages-list", args: []string{"list"},
			sql: "UPDATE decisions SET created_at = '2026-01-01T11:55:00+00:00' WHERE id = '36.1';" +
				" UPDATE decisions SET created_at = '2026-01-01T09:00:00+00:00' WHERE id = '7.3';" +
				" UPDATE decisions SET created_at = '2025-12-30T12:00:00+00:00' WHERE id = '9.1';" +
				" UPDATE decisions SET created_at = 'garbage' WHERE id = '10.1'"},
		gstep{name: "ages-list-narrow", args: []string{"list"}, env: []string{"COLUMNS=25"}},
		gstep{name: "no-sessions-list", args: []string{"list"}, rm: []string{"sessions.json"}},
		named("final-json", plain("list", "--json")),
		named("final-all", plain("list", "--all")),
		named("final-all-json", plain("list", "--all", "--json")),
		gstep{name: "vault-missing", args: []string{"list"}, env: []string{"ZEN_FLOW_DB=", "HOME=$ROOT/h1"}},
		gstep{name: "vault-no-key", args: []string{"list"}, env: []string{"ZEN_FLOW_DB=", "HOME=$ROOT/h2"},
			home: map[string]string{"h2/.claude/second-brain.json": `{"x": 1}`}},
		gstep{name: "vault-no-config", args: []string{"list"}, env: []string{"ZEN_FLOW_DB=", "HOME=$ROOT/h3"},
			home: map[string]string{"h3/.claude/second-brain.json": `{"vault": "$ROOT/v3"}`}},
		gstep{name: "vault-ok", args: []string{"list"}, env: []string{"ZEN_FLOW_DB=", "HOME=$ROOT/h4"},
			home: map[string]string{"h4/.claude/second-brain.json": `{"vault": "$ROOT/v4"}`,
				"v4/config/.keep": ""}},
		gstep{name: "vault-ask", args: []string{"ask", "1", "-q", "Vault?", "--session", "s1"},
			env: []string{"ZEN_FLOW_DB=", "HOME=$ROOT/h4"}},
	)
	return steps
}

func expand(s, root string) string { return strings.ReplaceAll(s, "$ROOT", root) }

func renderStep(i int, st gstep, r result, sends [][]string, root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "step %03d %s\nargs: %q\nexit: %d\n", i, st.name, st.args, r.code)
	if st.usage {
		b.WriteString("(usage error: output not compared)\n")
		return b.String()
	}
	fmt.Fprintf(&b, "-- stdout --\n%s-- stderr --\n%s-- sends --\n", r.stdout, r.stderr)
	for _, s := range sends {
		fmt.Fprintf(&b, "%q\n", s)
	}
	return strings.ReplaceAll(b.String(), root, "$ROOT")
}

func goldenPath(i int, st gstep) string {
	return filepath.Join("testdata", "golden", fmt.Sprintf("%03d-%s.txt", i, st.name))
}

func TestGolden(t *testing.T) {
	f := newFake(t)
	prefix := []string{zenBin}
	if *goldenPython != "" {
		prefix = strings.Fields(*goldenPython)
	}
	base := []string{"ZEN_INBOX_NOW=" + goldenNow, "COLUMNS=80"}
	seen := 0
	for i, st := range goldenScenario() {
		for name, body := range st.files {
			f.write(name, body)
		}
		for _, name := range st.rm {
			f.remove(name)
		}
		for rel, body := range st.home {
			p := filepath.Join(f.root, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(expand(body, f.root)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if st.sql != "" {
			f.sqlite(st.sql)
		}
		env := append([]string(nil), base...)
		for _, e := range st.env {
			env = append(env, expand(e, f.root))
		}
		r := run(f.cmd(env, append(append([]string(nil), prefix...), st.args...)...))
		all := f.sends()
		got := renderStep(i, st, r, all[seen:], f.root)
		seen = len(all)
		path := goldenPath(i, st)
		if *goldenPython != "" {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("step %d %s: %v", i, st.name, err)
		}
		if got != string(want) {
			t.Errorf("step %03d %s differs from %s\n--- want\n%s\n--- got\n%s", i, st.name, path, want, got)
		}
	}
}
