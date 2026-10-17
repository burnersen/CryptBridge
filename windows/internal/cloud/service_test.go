// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import "testing"

func TestHasSignIn(t *testing.T) {
	cases := []struct {
		params map[string]string
		want   bool
	}{
		{nil, false},
		{map[string]string{"type": Dropbox, "token": fakeToken}, true},
		{map[string]string{"type": Dropbox}, false},
		{map[string]string{"type": Mega, "token": fakeToken}, false},
		{map[string]string{"type": Mega, megaSessionID: "session-1"}, false},
		{map[string]string{"type": Mega, megaSessionID: "session-1", megaMasterKey: "a2V5"}, true},
		{map[string]string{"type": "box", "token": fakeToken}, true}, // a service this CryptBridge does not know
	}
	for _, c := range cases {
		if got := HasSignIn(c.params); got != c.want {
			t.Errorf("%v: got %v, want %v", c.params, got, c.want)
		}
	}
}

// Only MEGA lacks change reports; the drives of the others keep listings
// for a day.
func TestNoChangeReports(t *testing.T) {
	for _, s := range Services() {
		if want := s.Type == Mega; s.NoChangeReports != want {
			t.Errorf("%s: NoChangeReports = %v, want %v", s.Title, s.NoChangeReports, want)
		}
	}
}

// Every service has a sign-in way the window knows and settings that hold
// its sign-in.
func TestSignInWays(t *testing.T) {
	for _, s := range Services() {
		switch s.SignIn {
		case SignInBrowser:
		case SignInPassword:
			if s.App != AppNotOffered {
				t.Errorf("%s: an own app makes no sense without the browser sign-in", s.Title)
			}
		default:
			t.Errorf("%s: unknown sign-in way %q", s.Title, s.SignIn)
		}
		if len(s.signedInBy) == 0 {
			t.Errorf("%s: no settings hold the sign-in", s.Title)
		}
	}
}
