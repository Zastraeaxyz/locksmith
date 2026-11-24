package feature

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"locksmith/internal/store"
)

func TestNormalizeHandle(t *testing.T) {
	tests := map[string]string{
		"zastraea":    "zastraea",
		"Zastraea#0":  "zastraea",
		"  Foo#1234 ": "foo",
		"":            "",
		"#0":          "",
	}
	for raw, want := range tests {
		if got := normalizeHandle(raw); got != want {
			t.Errorf("normalizeHandle(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestIsSnowflake(t *testing.T) {
	if !isSnowflake("1574588415783997481") {
		t.Fatal("valid snowflake rejected")
	}
	for _, value := range []string{"", "123", "abc", "1574588415783997481x", "1234567890123456789012"} {
		if isSnowflake(value) {
			t.Errorf("isSnowflake(%q) = true", value)
		}
	}
}

func TestDocumentModalPrefill(t *testing.T) {
	data := documentModal(store.DocumentProfile, strings.Repeat("x", 5000))
	if data.CustomID != "profile:edit" {
		t.Fatalf("custom id = %q", data.CustomID)
	}
	if len(data.Components) != 1 {
		t.Fatalf("components = %d", len(data.Components))
	}
	row, ok := data.Components[0].(*discordgo.ActionsRow)
	if !ok || len(row.Components) != 1 {
		t.Fatalf("row = %#v", data.Components[0])
	}
	input, ok := row.Components[0].(*discordgo.TextInput)
	if !ok {
		t.Fatalf("input = %#v", row.Components[0])
	}
	if got := len([]rune(input.Value)); got != documentMaxLength {
		t.Fatalf("prefill length = %d, want %d", got, documentMaxLength)
	}
	if input.Style != discordgo.TextInputParagraph {
		t.Fatalf("style = %v", input.Style)
	}
	if input.MaxLength != documentMaxLength {
		t.Fatalf("max length = %d", input.MaxLength)
	}
	if input.Required {
		t.Fatal("input should allow an empty submission to clear the document")
	}
}

func TestResolveDocumentUser(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.SaveCredential(ctx, &store.Credential{
		DiscordUserID:  "user-1",
		ChastiSafeID:   "1574588415783997481",
		ChastiSafeUser: "zastraea#0",
		Secret:         []byte("sealed"),
	}); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	if err := st.SaveDocument(ctx, &store.UserDocument{
		DiscordUserID: "user-2",
		Kind:          store.DocumentProfile,
		Handle:        "other",
		Content:       "x",
	}); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}

	f := New(Options{Store: st})

	id, ok, err := f.resolveDocumentUser(ctx, "zastraea#0")
	if err != nil || !ok || id != "user-1" {
		t.Fatalf("ChastiSafe name = %q, %v, %v", id, ok, err)
	}
	id, ok, err = f.resolveDocumentUser(ctx, "other")
	if err != nil || !ok || id != "user-2" {
		t.Fatalf("handle = %q, %v, %v", id, ok, err)
	}
	id, ok, err = f.resolveDocumentUser(ctx, "1574588415783997481")
	if err != nil || !ok || id != "1574588415783997481" {
		t.Fatalf("id = %q, %v, %v", id, ok, err)
	}
	if _, ok, err := f.resolveDocumentUser(ctx, "nobody"); err != nil || ok {
		t.Fatalf("unknown = %v, %v", ok, err)
	}
}

func TestDocumentEmbed(t *testing.T) {
	profile := documentEmbed(store.DocumentProfile, "zastraea", strings.Repeat("x", 5000))
	if profile.Title != "zastraea's profile" {
		t.Fatalf("title = %q", profile.Title)
	}
	if got := len([]rune(profile.Description)); got != documentMaxLength {
		t.Fatalf("description length = %d, want %d", got, documentMaxLength)
	}

	rules := documentEmbed(store.DocumentRules, "zastraea", "line one\nline two")
	if rules.Title != "zastraea's rules of play" {
		t.Fatalf("rules title = %q", rules.Title)
	}
	if rules.Description != "line one\nline two" {
		t.Fatalf("rules description = %q", rules.Description)
	}
	if rules.Color == profile.Color {
		t.Fatal("profile and rules should use different colors")
	}
}
