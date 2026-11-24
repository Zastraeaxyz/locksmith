package feature

import (
	"strings"
	"testing"
)

func TestRenderChangelog(t *testing.T) {
	markdown := "# Changelog\n\n" +
		"Notable changes to Locksmith, newest first.\n\n" +
		"## 2026-11-24\n\n" +
		"### Added\n\n" +
		"- `/changelog` shows recent changes inside Discord.\n" +
		"- Private `/history` replies include each completed lock's code.\n\n" +
		"### Changed\n\n" +
		"- Draw notification text points at the button.\n"

	text := renderChangelog(markdown, changelogDescriptionLimit)
	for _, want := range []string{
		"**2026-11-24**",
		"*Added*",
		"• `/changelog` shows recent changes inside Discord.",
		"*Changed*",
		"• Draw notification text points at the button.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("changelog missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"# Changelog", "Notable changes to Locksmith", "###", "## "} {
		if strings.Contains(text, bad) {
			t.Errorf("changelog should not contain %q:\n%s", bad, text)
		}
	}
}

func TestRenderChangelogEmpty(t *testing.T) {
	if got := renderChangelog("", 100); got != "" {
		t.Fatalf("empty changelog = %q", got)
	}
	if got := renderChangelog("# Changelog\n\nNo dated sections yet.", 100); got != "" {
		t.Fatalf("intro-only changelog = %q", got)
	}
}

func TestRenderChangelogTruncatesOnLineBoundary(t *testing.T) {
	var b strings.Builder
	b.WriteString("## 2026-11-24\n\n")
	for index := 0; index < 100; index++ {
		b.WriteString("- a change that should not be cut in the middle\n")
	}

	text := renderChangelog(b.String(), 120)
	if got := len([]rune(text)); got > 120 {
		t.Fatalf("changelog length = %d, want at most 120", got)
	}
	if !strings.Contains(text, "Older changes are in the repository changelog.") {
		t.Fatalf("truncated changelog has no hint:\n%s", text)
	}
	if strings.HasSuffix(strings.TrimSpace(strings.Split(text, "Older changes")[0]), "midd") {
		t.Fatalf("changelog was cut mid-line:\n%s", text)
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "• ") && !strings.HasPrefix(line, "• a change that should not be cut in the middle") {
			t.Fatalf("changelog has a partial bullet %q:\n%s", line, text)
		}
	}
}
