package am

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type hit struct{ path, body, click string }

func ntfyServer(t *testing.T, status int) (*httptest.Server, func() []hit) {
	t.Helper()
	var mu sync.Mutex
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, hit{r.Method + " " + r.URL.Path, string(b), r.Header.Get("Click")})
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []hit {
		mu.Lock()
		defer mu.Unlock()
		return append([]hit(nil), hits...)
	}
}

func TestPushPostsMinimalBody(t *testing.T) {
	srv, got := ntfyServer(t, 200)
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("ZEN_NTFY_URL", srv.URL+"/")
	t.Setenv("ZEN_NTFY_TOPIC", "secret-topic")
	t.Setenv("ZEN_NTFY_CLICK", "")
	Exec{}.Push("#7 · 2 open · PROD")
	h := got()
	if len(h) != 1 || h[0].path != "POST /secret-topic" || h[0].body != "#7 · 2 open · PROD" || h[0].click != "" {
		t.Fatalf("got %+v", h)
	}
}

func TestPushSendsClickHeaderWhenSet(t *testing.T) {
	srv, got := ntfyServer(t, 200)
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("ZEN_NTFY_URL", srv.URL)
	t.Setenv("ZEN_NTFY_TOPIC", "t")
	t.Setenv("ZEN_NTFY_CLICK", "blink://open")
	Exec{}.Push("✉ 1 unread")
	if h := got(); len(h) != 1 || h[0].click != "blink://open" {
		t.Fatalf("got %+v", h)
	}
}

func TestPushUnsetTopicOrSSHSendsNothing(t *testing.T) {
	srv, got := ntfyServer(t, 200)
	t.Setenv("ZEN_NTFY_URL", srv.URL)
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("ZEN_NTFY_TOPIC", "")
	Exec{}.Push("x")
	t.Setenv("ZEN_NTFY_TOPIC", "t")
	t.Setenv("SSH_CONNECTION", "1.2.3.4 5 6.7.8.9 22")
	Exec{}.Push("x")
	if h := got(); len(h) != 0 {
		t.Fatalf("sent %+v", h)
	}
}

func TestPushServerErrorAndHangDoNotBlock(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("ZEN_NTFY_TOPIC", "t")
	srv, _ := ntfyServer(t, 500)
	t.Setenv("ZEN_NTFY_URL", srv.URL)
	Exec{}.Push("x")

	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(hang.Close)
	t.Cleanup(func() { close(release) })
	t.Setenv("ZEN_NTFY_URL", hang.URL)
	old := pushTimeout
	pushTimeout = 100 * time.Millisecond
	t.Cleanup(func() { pushTimeout = old })
	start := time.Now()
	Exec{}.Push("x")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("hung push took %v", d)
	}
}

func TestSSHSuppressesBannerAndBell(t *testing.T) {
	data := fakes(t, "[]")
	t.Setenv("SSH_CONNECTION", "1.2.3.4 5 6.7.8.9 22")
	Exec{OS: "darwin"}.Notify("T", "body")
	Exec{OS: "linux"}.Notify("T", "body")
	Exec{OS: "darwin"}.Bell("stuck")
	time.Sleep(200 * time.Millisecond)
	if got := notified(t, data); len(got) != 0 {
		t.Fatalf("banner posted over ssh: %q", got)
	}
	if _, err := readBells(data); err == nil {
		t.Fatal("zen-bell rang over ssh")
	}
}
