package feature

import (
	"errors"
	"fmt"
	"testing"

	cs "github.com/zastraeaxyz/chastisafe-go"
)

func TestLoginFailureReason(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errNotOwner, "not owner"},
		{cs.ErrUnauthorized, "rejected"},
		{errNotConfigured, "unconfigured"},
		{errors.New("network down"), "unreachable"},
		{fmt.Errorf("wrapped: %w", cs.ErrUnauthorized), "rejected"},
	}
	for _, tc := range cases {
		if got := loginFailureReason(tc.err); got != tc.want {
			t.Errorf("loginFailureReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
