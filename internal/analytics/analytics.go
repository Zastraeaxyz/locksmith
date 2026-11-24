// Package analytics sends anonymous usage events to an Umami instance.
//
// Locksmith is a Discord bot, so it cannot use Umami's browser script. It posts
// events to the server-side /api/send endpoint instead. Only interaction labels
// such as "/lock status" are sent; Discord user IDs, usernames, guild IDs,
// lock IDs, and stored documents never leave the bot.
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sendPath    = "/api/send"
	hostname    = "locksmith"
	userAgent   = "locksmith/1"
	eventURL    = "/"
	queueSize   = 64
	sendTimeout = 5 * time.Second
	closeWait   = 5 * time.Second
)

// Options configures the tracker. Both SiteID and Endpoint must be set;
// otherwise New returns nil and tracking is a no-op.
type Options struct {
	SiteID   string
	Endpoint string
	Logger   *slog.Logger
	Client   *http.Client
}

// Tracker buffers events and posts them from a single background worker so an
// interaction handler never waits on the network.
type Tracker struct {
	siteID   string
	endpoint string
	log      *slog.Logger
	client   *http.Client

	events    chan event
	done      chan struct{}
	closeOnce sync.Once
}

type event struct {
	name string
	data map[string]any
}

// New builds a tracker, or returns nil when analytics is not configured.
func New(opts Options) *Tracker {
	siteID := strings.TrimSpace(opts.SiteID)
	endpoint := normalizeEndpoint(opts.Endpoint)
	if siteID == "" || endpoint == "" {
		return nil
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: sendTimeout}
	}

	t := &Tracker{
		siteID:   siteID,
		endpoint: endpoint,
		log:      opts.Logger,
		client:   opts.Client,
		events:   make(chan event, queueSize),
		done:     make(chan struct{}),
	}
	go t.worker()
	return t
}

// Track queues an event. It never blocks: when the queue is full the event is
// dropped.
func (t *Tracker) Track(name string, data map[string]any) {
	if t == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	select {
	case t.events <- event{name: name, data: data}:
	default:
		t.log.Debug("analytics queue full; dropping event", "event", name)
	}
}

// Close drains the queue for a short while and stops the worker.
func (t *Tracker) Close() {
	if t == nil {
		return
	}
	t.closeOnce.Do(func() {
		close(t.events)
		select {
		case <-t.done:
		case <-time.After(closeWait):
			t.log.Debug("analytics did not drain before shutdown")
		}
	})
}

func (t *Tracker) worker() {
	defer close(t.done)
	for e := range t.events {
		t.send(e)
	}
}

func (t *Tracker) send(e event) {
	payload := map[string]any{
		"website":  t.siteID,
		"hostname": hostname,
		"url":      eventURL,
		"name":     e.name,
	}
	if len(e.data) > 0 {
		payload["data"] = e.data
	}
	body, err := json.Marshal(map[string]any{
		"type":    "event",
		"payload": payload,
	})
	if err != nil {
		t.log.Debug("encoding analytics event", "event", e.name, "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint+sendPath, bytes.NewReader(body))
	if err != nil {
		t.log.Debug("building analytics request", "event", e.name, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := t.client.Do(req)
	if err != nil {
		t.log.Debug("sending analytics event", "event", e.name, "err", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= http.StatusMultipleChoices {
		t.log.Debug("analytics rejected event", "event", e.name, "status", resp.StatusCode)
	}
}

// normalizeEndpoint accepts a bare host and adds the https scheme.
func normalizeEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	return strings.TrimRight(raw, "/")
}
