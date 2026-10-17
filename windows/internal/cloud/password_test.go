// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"context"
	"strings"
	"testing"
)

func TestNewCredentialsCleansPastedText(t *testing.T) {
	creds, err := NewCredentials("  name@example.com\t", " pass word ", " 123 456 ")
	if err != nil {
		t.Fatal(err)
	}
	want := Credentials{User: "name@example.com", Password: " pass word ", Code: "123456"}
	if creds != want {
		t.Fatalf("got %+v, want %+v (the password exactly as typed)", creds, want)
	}
	if creds, err := NewCredentials("name@example.com", "secret", ""); err != nil || creds.Code != "" {
		t.Fatalf("without two-factor code: got %+v, %v", creds, err)
	}
}

func TestNewCredentialsRejectsWhatCannotWork(t *testing.T) {
	cases := []struct{ user, password, code string }{
		{"", "secret", ""},
		{"name@example.com", "", ""},
		{"name.example.com", "secret", ""},
		{"name @example.com", "secret", ""},
		{strings.Repeat("a", maxUserLength) + "@example.com", "secret", ""},
		{"name@example.com", "secret", "12345"},
		{"name@example.com", "secret", "1234567"},
		{"name@example.com", "secret", "12a456"},
	}
	for _, c := range cases {
		if creds, err := NewCredentials(c.user, c.password, c.code); err == nil {
			t.Errorf("%q / %q / %q: accepted as %+v", c.user, c.password, c.code, creds)
		}
	}
}

func TestSessionOfKeepsNoPassword(t *testing.T) {
	mega := mustService(t, Mega)
	signedIn := map[string]string{
		"type": Mega, megaUser: "name@example.com", megaPassword: "obscured", megaCode: "123456",
		megaSessionID: "session-1", megaMasterKey: "a2V5",
	}
	session, err := sessionOf(mega, "name@example.com", signedIn)
	if err != nil {
		t.Fatal(err)
	}
	if len(session) != 3 || session[megaSessionID] != "session-1" || session[megaMasterKey] != "a2V5" {
		t.Fatalf("session = %v; want user, session and master key only", session)
	}
	if !strings.HasPrefix(session[megaUser], "name@example.com (session ") {
		t.Fatalf("user = %q; want the e-mail address with a mark of the session", session[megaUser])
	}
	if got := SessionUser(session); got != "name@example.com" {
		t.Fatalf("SessionUser = %q; want the e-mail address without the mark", got)
	}

	// The mark depends on the session alone: the same session keeps its
	// name, a new one gets a new name and so a new sign-in in rclone.
	again, _ := sessionOf(mega, "name@example.com", signedIn)
	signedIn[megaSessionID] = "session-2"
	next, _ := sessionOf(mega, "name@example.com", signedIn)
	if again[megaUser] != session[megaUser] || next[megaUser] == session[megaUser] {
		t.Fatalf("names: %q, again %q, new session %q", session[megaUser], again[megaUser], next[megaUser])
	}
}

func TestSessionOfNeedsASession(t *testing.T) {
	mega := mustService(t, Mega)
	for _, params := range []map[string]string{
		{megaUser: "name@example.com"},
		{megaSessionID: "session-1"},
		{megaMasterKey: "a2V5"},
	} {
		if session, err := sessionOf(mega, "name@example.com", params); err == nil {
			t.Errorf("%v: got session %v", params, session)
		}
	}
}

func TestExplainLogInFailure(t *testing.T) {
	// rclone 1.75.2 wraps go-mega's texts as "couldn't login: <text>".
	cases := []struct{ message, want string }{
		{"couldn't login: Object (typically, node or user) not found", "password is wrong"},
		{"couldn't login: Multi-factor authentication required", "six-digit code"},
		{"couldn't login: The upload failed. Please restart it from scratch", "did not accept the two-factor code"},
		{"couldn't login: User blocked", "blocked the account"},
		{"couldn't login: Rate limit reached", "try again later"},
		{"couldn't login: Try again", "try again later"},
		{"couldn't login: Resource temporarily not available, please try again later", "MEGA asks to wait"},
	}
	for _, c := range cases {
		if got := explainLogInFailure(c.message); !strings.Contains(got, c.want) {
			t.Errorf("%q: got %q, want it to say %q", c.message, got, c.want)
		}
	}
	other := `couldn't login: Post "https://g.api.mega.co.nz/cs": dial tcp: lookup g.api.mega.co.nz: no such host`
	if got := explainLogInFailure(other); got != other {
		t.Errorf("an unknown reason was changed to %q", got)
	}
}

func TestLogInOnlyForPasswordServices(t *testing.T) {
	// Fails before any rclone starts, so no rclone is needed.
	creds := Credentials{User: "name@example.com", Password: "secret"}
	if _, err := LogIn(context.Background(), "rclone-not-needed.exe", mustService(t, Dropbox), creds); err == nil {
		t.Fatal("Dropbox was signed in to with a password")
	}
}
