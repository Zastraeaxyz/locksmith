package feature

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/store"
)

const (
	testUserID            = "990454274196668516"
	testAppID             = "1574588415783997481"
	testChannelID         = "3000000000000000001"
	testInteractionID     = "1000000000000000000"
	testInteractionToken  = "interaction-token"
	testChastiSafeBaseURL = "https://chastisafe.test/api-v1"
	testDiscordAPI        = "https://discord.com/api/v9"
)

var snapshotNow = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

// handlerFunc is the shape shared by every command and modal handler.
type handlerFunc func(context.Context, *discordgo.Session, *discordgo.InteractionCreate) error

// discordCall is one outbound Discord API request, without headers or tokens.
type discordCall struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

type fakeResponse struct {
	status int
	body   []byte
}

// fakeAPI serves recorded Discord and ChastiSafe responses. Every request must
// have a matching route, so an unmocked call fails instead of reaching the
// network.
type fakeAPI struct {
	mu     sync.Mutex
	routes map[string]fakeResponse
	calls  []discordCall
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{routes: make(map[string]fakeResponse)}
}

// serve registers a response for one method and absolute URL. A zero status
// means 200, and an empty fixture means an empty body.
func (a *fakeAPI) serve(t *testing.T, method, rawURL string, status int, fixture string) {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing fake route %q: %v", rawURL, err)
	}
	if status == 0 {
		status = http.StatusOK
	}

	var body []byte
	if fixture != "" {
		body = readFixture(t, fixture)
	}

	a.mu.Lock()
	a.routes[method+" "+parsed.Host+parsed.Path] = fakeResponse{status: status, body: body}
	a.mu.Unlock()
}

func (a *fakeAPI) resetCalls() {
	a.mu.Lock()
	a.calls = nil
	a.mu.Unlock()
}

func (a *fakeAPI) transcript() []discordCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]discordCall(nil), a.calls...)
}

// RoundTrip serves a fixture and records outbound Discord traffic.
func (a *fakeAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	var err error
	if req.Body != nil {
		body, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s %s: %w", req.Method, req.URL, err)
		}
	}

	key := req.Method + " " + req.URL.Host + req.URL.Path

	a.mu.Lock()
	response, ok := a.routes[key]
	if req.URL.Host == "discord.com" {
		call := discordCall{Method: req.Method, Path: req.URL.RequestURI()}
		if len(body) > 0 {
			call.Body = append(json.RawMessage(nil), body...)
		}
		a.calls = append(a.calls, call)
	}
	a.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("snapshot: unexpected request %s %s", req.Method, req.URL.String())
	}

	header := make(http.Header)
	if len(response.body) > 0 {
		header.Set("Content-Type", "application/json")
	}
	return &http.Response{
		StatusCode: response.status,
		Status:     strconv.Itoa(response.status) + " " + http.StatusText(response.status),
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(response.body)),
		Request:    req,
	}, nil
}

// snapshotEnv is one isolated test run: a temp store, a fake Discord session,
// and a fake ChastiSafe API.
type snapshotEnv struct {
	feature *Feature
	store   *store.Store
	session *discordgo.Session
	api     *fakeAPI
}

func newSnapshotEnv(t *testing.T) *snapshotEnv {
	t.Helper()
	ctx := context.Background()

	st, box := openAccountTestStore(t)

	sealed, err := box.Seal([]byte("secret-code"))
	if err != nil {
		t.Fatalf("sealing fixture credential: %v", err)
	}
	if err := st.SaveCredential(ctx, &store.Credential{
		DiscordUserID:  testUserID,
		ChastiSafeID:   testUserID,
		ChastiSafeUser: "testuser#0",
		Secret:         sealed,
	}); err != nil {
		t.Fatalf("saving fixture credential: %v", err)
	}

	api := newFakeAPI()
	registerDiscordFixtures(t, api)
	registerChastiSafeFixtures(t, api)

	f := New(Options{
		Store:   st,
		Box:     box,
		BaseURL: testChastiSafeBaseURL,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return snapshotNow },
	})
	f.accounts.newClient = func(opts cs.Options) (*cs.Client, error) {
		opts.HTTPClient = &http.Client{Transport: api}
		return cs.New(opts)
	}

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: api}
	session.State.User = &discordgo.User{ID: testAppID, Username: "Locksmith"}

	if err := f.loadApplicationEmojis(ctx, session, &discordgo.Ready{
		User: &discordgo.User{ID: testAppID},
	}); err != nil {
		t.Fatalf("loading fixture emoji: %v", err)
	}
	api.resetCalls()

	return &snapshotEnv{feature: f, store: st, session: session, api: api}
}

func registerDiscordFixtures(t *testing.T, api *fakeAPI) {
	t.Helper()

	api.serve(t, http.MethodPost,
		testDiscordAPI+"/interactions/"+testInteractionID+"/"+testInteractionToken+"/callback",
		http.StatusNoContent, "")
	api.serve(t, http.MethodPatch,
		testDiscordAPI+"/webhooks/"+testAppID+"/"+testInteractionToken+"/messages/@original",
		http.StatusOK, "discord/message.json")
	api.serve(t, http.MethodDelete,
		testDiscordAPI+"/webhooks/"+testAppID+"/"+testInteractionToken+"/messages/@original",
		http.StatusNoContent, "")
	api.serve(t, http.MethodPost,
		testDiscordAPI+"/webhooks/"+testAppID+"/"+testInteractionToken,
		http.StatusOK, "discord/message.json")
	api.serve(t, http.MethodPost,
		testDiscordAPI+"/channels/"+testChannelID+"/messages",
		http.StatusOK, "discord/message.json")
	api.serve(t, http.MethodPatch,
		testDiscordAPI+"/channels/"+testChannelID+"/messages/4000000000000000001",
		http.StatusOK, "discord/message.json")
	api.serve(t, http.MethodGet,
		testDiscordAPI+"/applications/"+testAppID+"/emojis",
		http.StatusOK, "discord/emojis.json")
}

func registerChastiSafeFixtures(t *testing.T, api *fakeAPI) {
	t.Helper()

	api.serve(t, http.MethodGet, testChastiSafeBaseURL+"/who", http.StatusOK, "chastisafe/who.json")
	api.serve(t, http.MethodGet, testChastiSafeBaseURL+"/profile", http.StatusOK, "chastisafe/profile.json")
	api.serve(t, http.MethodGet, testChastiSafeBaseURL+"/locks", http.StatusOK, "chastisafe/locks.json")
	api.serve(t, http.MethodGet, testChastiSafeBaseURL+"/loaded", http.StatusOK, "chastisafe/loaded.json")
	api.serve(t, http.MethodGet, testChastiSafeBaseURL+"/recent", http.StatusOK, "chastisafe/recent.json")
	api.serve(t, http.MethodPost, testChastiSafeBaseURL+"/log", http.StatusOK, "chastisafe/log.json")
}

// snapshot runs one handler and compares its Discord transcript with a golden.
func (e *snapshotEnv) snapshot(t *testing.T, golden, interaction string, handler handlerFunc) {
	t.Helper()

	e.api.resetCalls()
	i := loadInteraction(t, interaction)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := handler(ctx, e.session, i); err != nil {
		t.Fatalf("handler: %v", err)
	}
	e.assertGolden(t, golden)
}

func (e *snapshotEnv) assertGolden(t *testing.T, name string) {
	t.Helper()

	got, err := json.MarshalIndent(e.api.transcript(), "", "  ")
	if err != nil {
		t.Fatalf("marshaling transcript: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "golden", name+".json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating golden directory: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v\nRun UPDATE_GOLDEN=1 go test ./internal/feature -run TestSnapshot to create it.", path, err)
	}
	if jsonEqual(want, got) {
		return
	}
	t.Fatalf("golden %s does not match.\n%s\nRun UPDATE_GOLDEN=1 go test ./internal/feature -run TestSnapshot to update it.", path, diffLines(want, got))
}

func loadInteraction(t *testing.T, name string) *discordgo.InteractionCreate {
	t.Helper()

	data := readFixture(t, "discord/"+name+".json")
	var interaction discordgo.InteractionCreate
	if err := json.Unmarshal(data, &interaction); err != nil {
		t.Fatalf("unmarshaling interaction %s: %v", name, err)
	}
	if interaction.Interaction == nil {
		t.Fatalf("interaction %s has no payload", name)
	}
	return &interaction
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	path := filepath.Join("testdata", filepath.FromSlash(name))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", path, err)
	}
	return data
}

func jsonEqual(want, got []byte) bool {
	var wantValue, gotValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		return bytes.Equal(want, got)
	}
	if err := json.Unmarshal(got, &gotValue); err != nil {
		return bytes.Equal(want, got)
	}
	return reflect.DeepEqual(wantValue, gotValue)
}

// diffLines prints the first differing region with a little context.
func diffLines(want, got []byte) string {
	wantLines := strings.Split(string(want), "\n")
	gotLines := strings.Split(string(got), "\n")

	prefix := 0
	for prefix < len(wantLines) && prefix < len(gotLines) && wantLines[prefix] == gotLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(wantLines)-prefix && suffix < len(gotLines)-prefix &&
		wantLines[len(wantLines)-1-suffix] == gotLines[len(gotLines)-1-suffix] {
		suffix++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "first difference at line %d\n", prefix+1)

	start := max(0, prefix-3)
	for idx := start; idx < prefix; idx++ {
		fmt.Fprintf(&b, "  %s\n", wantLines[idx])
	}
	for idx := prefix; idx < len(wantLines)-suffix; idx++ {
		fmt.Fprintf(&b, "- %s\n", wantLines[idx])
	}
	for idx := prefix; idx < len(gotLines)-suffix; idx++ {
		fmt.Fprintf(&b, "+ %s\n", gotLines[idx])
	}

	end := min(len(wantLines)-suffix, prefix+3)
	for idx := len(wantLines) - suffix; idx < end; idx++ {
		fmt.Fprintf(&b, "  %s\n", wantLines[idx])
	}
	return b.String()
}
