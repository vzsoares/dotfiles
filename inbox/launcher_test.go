package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// launcherEnv copies the module to a temp dir so touching sources never
// edits the repo, and puts a logging `go` wrapper first on PATH.
type launcherEnv struct {
	t      *testing.T
	src    string
	tools  string
	golog  string
	cache  string
	env    []string
	realGo string
}

func newLauncher(t *testing.T) *launcherEnv {
	t.Helper()
	root := t.TempDir()
	l := &launcherEnv{t: t, src: filepath.Join(root, "inbox"), tools: filepath.Join(root, "tools"),
		golog: filepath.Join(root, "go.log"), cache: filepath.Join(root, "cache")}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == "testdata" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(l.src, p), 0o755)
		}
		if !strings.HasSuffix(p, ".go") && p != "go.mod" && p != "go.sum" && p != "zen-inbox" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if p == "zen-inbox" {
			mode = 0o755
		}
		return os.WriteFile(filepath.Join(l.src, p), raw, mode)
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.realGo, err = exec.LookPath("go"); err != nil {
		t.Fatal(err)
	}
	// Only the tools the launcher uses, so a go in /usr/bin cannot leak in.
	if err := os.MkdirAll(l.tools, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"bash", "dirname", "readlink", "find", "mkdir", "mktemp", "touch", "mv", "rm"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if err := os.Symlink(p, filepath.Join(l.tools, tool)); err != nil {
			t.Fatal(err)
		}
	}
	goenv := map[string]string{}
	for _, k := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT"} {
		out, err := exec.Command("go", "env", k).Output()
		if err != nil {
			t.Fatal(err)
		}
		goenv[k] = strings.TrimSpace(string(out))
	}
	l.env = []string{"HOME=" + filepath.Join(root, "home"), "XDG_CACHE_HOME=" + l.cache,
		"XDG_DATA_HOME=" + filepath.Join(root, "data"), "ZEN_FLOW_DB=" + filepath.Join(root, "zen-flow.db"),
		"GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly"}
	for k, v := range goenv {
		l.env = append(l.env, k+"="+v)
	}
	return l
}

// wrapper writes a `go` that logs each build and runs the real one.
func (l *launcherEnv) wrapper(path string) {
	l.t.Helper()
	body := "#!/usr/bin/env bash\necho \"$@\" >>" + l.golog + "\nexec " + l.realGo + " \"$@\"\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		l.t.Fatal(err)
	}
}

func (l *launcherEnv) run(path string, args ...string) result {
	c := exec.Command(filepath.Join(l.src, "zen-inbox"), args...)
	c.Env = append(append([]string(nil), l.env...), "PATH="+path)
	return run(c)
}

func (l *launcherEnv) builds() int {
	raw, err := os.ReadFile(l.golog)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		l.t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "build ") {
			n++
		}
	}
	return n
}

func TestLauncherRebuildsOnlyWhenSourcesChange(t *testing.T) {
	l := newLauncher(t)
	bin := filepath.Join(l.t.TempDir(), "gobin")
	l.wrapper(filepath.Join(bin, "go"))
	path := bin + ":" + l.tools
	want := func(builds int) {
		t.Helper()
		r := l.run(path, "list")
		if r.code != 0 || r.stdout != "inbox empty\n" {
			t.Fatalf("launcher: %+v", r)
		}
		if got := l.builds(); got != builds {
			t.Fatalf("%d builds, want %d", got, builds)
		}
	}
	want(1)
	want(1)
	later := time.Now()
	if err := os.Chtimes(filepath.Join(l.src, "main.go"), later, later); err != nil {
		t.Fatal(err)
	}
	want(2)
	want(2)
	entries, _ := os.ReadDir(filepath.Join(l.cache, "zen", "bin"))
	if len(entries) != 1 {
		t.Fatalf("temp files left in the cache: %v", entries)
	}
}

func TestLauncherConcurrentColdStarts(t *testing.T) {
	l := newLauncher(t)
	bin := filepath.Join(l.t.TempDir(), "gobin")
	l.wrapper(filepath.Join(bin, "go"))
	var wg sync.WaitGroup
	results := make([]result, 2)
	for i := range results {
		wg.Go(func() { results[i] = l.run(bin+":"+l.tools, "list") })
	}
	wg.Wait()
	for i, r := range results {
		if r.code != 0 || r.stdout != "inbox empty\n" {
			t.Errorf("start %d: %+v", i, r)
		}
	}
}

func TestLauncherFindsGoInMiseShims(t *testing.T) {
	l := newLauncher(t)
	var data string
	for _, e := range l.env {
		if v, ok := strings.CutPrefix(e, "XDG_DATA_HOME="); ok {
			data = v
		}
	}
	l.wrapper(filepath.Join(data, "mise", "shims", "go"))
	if r := l.run(l.tools, "list"); r.code != 0 || l.builds() != 1 {
		t.Fatalf("mise shim not used: %+v builds %d", r, l.builds())
	}
}

func TestLauncherWithoutGoExits4(t *testing.T) {
	l := newLauncher(t)
	r := l.run(l.tools, "list")
	if r.code != 4 || !strings.Contains(r.stderr, "go not found") {
		t.Fatalf("%+v", r)
	}
}

func TestCrossCompileForManjaro(t *testing.T) {
	// -o names a directory: a bare build would write over the launcher.
	c := exec.Command("go", "build", "-o", t.TempDir()+string(os.PathSeparator), "./...")
	c.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("linux/amd64 build: %v\n%s", err, out)
	}
}
