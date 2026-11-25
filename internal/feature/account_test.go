package feature

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	cs "github.com/zastraeaxyz/chastisafe-go"

	"locksmith/internal/cryptobox"
	"locksmith/internal/store"
)

func openAccountTestStore(t *testing.T) (*store.Store, *cryptobox.Box) {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	box, err := cryptobox.New(bytes.Repeat([]byte("k"), cryptobox.KeySize))
	if err != nil {
		t.Fatalf("cryptobox.New: %v", err)
	}
	return st, box
}

func TestClientForUsesInjectedFactory(t *testing.T) {
	ctx := context.Background()
	st, box := openAccountTestStore(t)

	sealed, err := box.Seal([]byte("saved code"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := st.SaveCredential(ctx, &store.Credential{
		DiscordUserID: "user-1",
		ChastiSafeID:  "cs-1",
		Secret:        sealed,
	}); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}

	called := false
	f := &Feature{}
	f.accounts = accounts{
		store:     st,
		box:       box,
		baseURL:   "https://example.test",
		userAgent: "test-agent",
		newClient: func(opts cs.Options) (*cs.Client, error) {
			called = true
			if opts.Token != "saved code" {
				t.Fatalf("token = %q", opts.Token)
			}
			if opts.BaseURL != "https://example.test" || opts.UserAgent != "test-agent" {
				t.Fatalf("options = %+v", opts)
			}
			// A client is just configuration; no request is made here.
			return cs.New(cs.Options{Token: opts.Token, BaseURL: opts.BaseURL})
		},
	}

	client, cred, err := f.clientFor(ctx, "user-1")
	if !called {
		t.Fatal("client factory was not used")
	}
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	if client == nil {
		t.Fatal("clientFor returned no client")
	}
	if cred == nil || cred.DiscordUserID != "user-1" {
		t.Fatalf("credential = %+v", cred)
	}
}

func TestFailureReplyDropsRejectedCodeOnly(t *testing.T) {
	ctx := context.Background()
	st, box := openAccountTestStore(t)

	sealed, err := box.Seal([]byte("saved code"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := st.SaveCredential(ctx, &store.Credential{
		DiscordUserID: "user-1",
		ChastiSafeID:  "cs-1",
		Secret:        sealed,
	}); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}

	f := &Feature{}
	f.accounts = accounts{store: st, box: box}

	copy, dropped := f.failureReply(ctx, "user-1", cs.ErrNotAllowed)
	if dropped {
		t.Fatal("a non-auth failure must not drop the code")
	}
	if !strings.Contains(copy, "does not allow") {
		t.Fatalf("copy = %q", copy)
	}
	cred, err := st.Credential(ctx, "user-1")
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred == nil {
		t.Fatal("code was dropped by a non-auth failure")
	}

	copy, dropped = f.failureReply(ctx, "user-1", cs.ErrUnauthorized)
	if !dropped {
		t.Fatal("a rejected code must be dropped")
	}
	if !strings.Contains(copy, "/login") {
		t.Fatalf("copy = %q", copy)
	}
	cred, err = st.Credential(ctx, "user-1")
	if err != nil {
		t.Fatalf("Credential after rejection: %v", err)
	}
	if cred != nil {
		t.Fatalf("code still present: %+v", cred)
	}
}
