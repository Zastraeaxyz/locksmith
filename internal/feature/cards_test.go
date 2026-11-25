package feature

import "testing"

func TestClipAndEllipsize(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"clip over the limit", clip("abcdef", 3), "abc"},
		{"clip at the limit", clip("abc", 3), "abc"},
		{"clip under the limit", clip("ab", 3), "ab"},
		{"clip counts runes", clip("héllo", 2), "hé"},
		{"ellipsize over the limit", ellipsize("abcdef", 4), "abc…"},
		{"ellipsize at the limit", ellipsize("abc", 4), "abc"},
		{"ellipsize counts runes", ellipsize("héllo", 2), "h…"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
