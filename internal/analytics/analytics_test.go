package analytics

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewDisabledWithoutConfig(t *testing.T) {
	for _, opts := range []Options{
		{},
		{SiteID: "site"},
		{Endpoint: "analytics.test"},
		{SiteID: "  ", Endpoint: " "},
	} {
		if got := New(opts); got != nil {
			t.Errorf("New(%+v) = %v, want nil", opts, got)
		}
	}
}

func TestTrackPostsEvent(t *testing.T) {
	type request struct {
		Type    string `json:"type"`
		Payload struct {
			Website  string         `json:"website"`
			Hostname string         `json:"hostname"`
			URL      string         `json:"url"`
			Name     string         `json:"name"`
			Data     map[string]any `json:"data"`
		} `json:"payload"`
	}

	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != sendPath {
			t.Errorf("path = %q, want %q", r.URL.Path, sendPath)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("user agent = %q, want %q", got, userAgent)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		var got request
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		requests <- got
		_, _ = io.WriteString(w, `{"beep":"boop"}`)
	}))
	defer server.Close()

	tracker := New(Options{
		SiteID:   "site-id",
		Endpoint: " " + server.URL + "/ ",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if tracker == nil {
		t.Fatal("New returned nil with complete config")
	}
	tracker.Track("/lock status", map[string]any{"sub": "status"})
	tracker.Close()

	got := <-requests
	if got.Type != "event" {
		t.Errorf("type = %q, want event", got.Type)
	}
	if got.Payload.Website != "site-id" {
		t.Errorf("website = %q", got.Payload.Website)
	}
	if got.Payload.Hostname != hostname {
		t.Errorf("hostname = %q, want %q", got.Payload.Hostname, hostname)
	}
	if got.Payload.URL != eventURL {
		t.Errorf("url = %q, want %q", got.Payload.URL, eventURL)
	}
	if got.Payload.Name != "/lock status" {
		t.Errorf("name = %q", got.Payload.Name)
	}
	if got.Payload.Data["sub"] != "status" {
		t.Errorf("data = %v", got.Payload.Data)
	}
}

func TestTrackIgnoresEmptyName(t *testing.T) {
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
	}))
	defer server.Close()

	tracker := New(Options{
		SiteID:   "site-id",
		Endpoint: server.URL,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	tracker.Track("  ", nil)
	tracker.Close()

	select {
	case <-requests:
		t.Fatal("empty event name was sent")
	default:
	}
}

func TestTrackSurvivesFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	tracker := New(Options{
		SiteID:   "site-id",
		Endpoint: server.URL,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	tracker.Track("/login", nil)
	tracker.Close()
	server.Close()

	// A stopped server must not cause a panic or hang.
	failing := New(Options{
		SiteID:   "site-id",
		Endpoint: server.URL,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	failing.Track("/logout", nil)
	failing.Close()
}

func TestUserAgentPassesBotCheck(t *testing.T) {
	// Umami silently drops events whose user agent isbot reports as a bot.
	// isbot accepts a browser-shaped user agent, so keep that prefix.
	if !strings.HasPrefix(userAgent, "Mozilla/5.0 ") {
		t.Fatalf("user agent = %q, want a Mozilla/5.0 prefix so Umami accepts it", userAgent)
	}
	if !strings.Contains(userAgent, "Locksmith/") {
		t.Fatalf("user agent = %q, want a Locksmith token", userAgent)
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	cases := map[string]string{
		"":                                 "",
		"analytics.test":                   "https://analytics.test",
		"https://analytics.test/":          "https://analytics.test",
		"http://localhost:3000":            "http://localhost:3000",
		"  https://analytics.test/base/  ": "https://analytics.test/base",
	}
	for raw, want := range cases {
		if got := normalizeEndpoint(raw); got != want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNilTrackerIsNoop(t *testing.T) {
	var tracker *Tracker
	tracker.Track("/login", nil)
	tracker.Close()
}

func TestCacheTokenReused(t *testing.T) {
	headers := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("x-umami-cache")
		_, _ = io.WriteString(w, `{"cache":"token-1","sessionId":"session"}`)
	}))
	defer server.Close()

	tracker := New(Options{
		SiteID:   "site-id",
		Endpoint: server.URL,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	tracker.Track("/login", nil)
	tracker.Track("/logout", nil)
	tracker.Close()

	if got := <-headers; got != "" {
		t.Errorf("first request cache = %q, want empty", got)
	}
	if got := <-headers; got != "token-1" {
		t.Errorf("second request cache = %q, want token-1", got)
	}
}

func TestCacheTokenSurvivesBadResponse(t *testing.T) {
	tracker := New(Options{
		SiteID:   "site-id",
		Endpoint: "http://127.0.0.1:1",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	tracker.storeCache([]byte("not json"))
	if got := tracker.cacheToken(); got != "" {
		t.Errorf("cache = %q, want empty", got)
	}
	tracker.Close()
}
