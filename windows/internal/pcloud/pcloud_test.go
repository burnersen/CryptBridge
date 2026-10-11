// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package pcloud

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// authorizeOutput is what "rclone authorize pcloud" printed (measured
// 2026-10-10), with the token replaced.
const authorizeOutput = `2026/10/10 21:29:22 NOTICE: Make sure your Redirect URL is set to "http://localhost:53682/" in your custom config.
2026/10/10 21:29:22 NOTICE: If your browser doesn't open automatically go to the following link: http://127.0.0.1:53682/auth?state=UJZrH6HmJTbfTd1tBD1_DA
2026/10/10 21:29:22 NOTICE: Log in and authorize rclone for access
2026/10/10 21:29:22 NOTICE: Waiting for code...
2026/10/10 21:30:27 NOTICE: Got code
Paste the following into your remote machine --->
{"access_token":"TESTTOKEN","token_type":"bearer","expiry":"0001-01-01T00:00:00Z"}
<---End paste
`

func TestScanAuthorizeOutput(t *testing.T) {
	var links []string
	token, err := scanAuthorizeOutput(strings.NewReader(authorizeOutput), func(l string) { links = append(links, l) })
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"access_token":"TESTTOKEN","token_type":"bearer","expiry":"0001-01-01T00:00:00Z"}`; token != want {
		t.Fatalf("token = %q", token)
	}
	if len(links) != 1 || links[0] != "http://127.0.0.1:53682/auth?state=UJZrH6HmJTbfTd1tBD1_DA" {
		t.Fatalf("links = %v", links)
	}
}

func TestScanAuthorizeOutputWithoutToken(t *testing.T) {
	token, err := scanAuthorizeOutput(strings.NewReader("NOTICE: Waiting for code...\n"), nil)
	if err != nil || token != "" {
		t.Fatalf("got %q, %v; want no token", token, err)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		message string
		want    error
	}{
		// Measured messages (rclone 1.75.1).
		{"about call failed: pcloud error: Revoked 'access_token' provided, you need to re-authorize the application. (2095)", ErrSignInRevoked},
		{"about call failed: pcloud error: Invalid 'access_token' provided. (2094)", ErrSignInRejected},
		{"couldn't list files: pcloud error: Revoked 'access_token' provided, you need to re-authorize the application. (2095)", ErrSignInRevoked},
		{`about call failed: Post "https://eapi.pcloud.com/userinfo": dial tcp: lookup eapi.pcloud.com: no such host`, ErrUnreachable},
		// Built from pCloud's documented number groups.
		{"pcloud error: Too many login tries from this IP address. (4000)", ErrBusy},
		{"pcloud error: Internal error. Try again later. (5000)", ErrServerProblem},
	}
	for _, c := range cases {
		original := errors.New(c.message)
		got := Classify(original)
		if !errors.Is(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.message, got, c.want)
		}
		if !errors.Is(got, original) {
			t.Errorf("%q: rclone's error got lost", c.message)
		}
		if got.Error() != c.want.Error() {
			t.Errorf("%q: message %q, want only the explanation", c.message, got.Error())
		}
	}
}

func TestClassifyLeavesOtherErrorsAlone(t *testing.T) {
	for _, message := range []string{
		"directory not found",
		"pcloud error: Directory does not exist. (2005)", // a 2xxx error that is not about the sign-in
	} {
		original := errors.New(message)
		if got := Classify(original); got != original {
			t.Errorf("%q changed to %v", message, got)
		}
	}
	if Classify(nil) != nil {
		t.Error("nil must stay nil")
	}
	once := Classify(errors.New("pcloud error: Invalid 'access_token' provided. (2094)"))
	if Classify(once) != once {
		t.Error("classifying twice must not wrap again")
	}
}

func TestIsSignInProblem(t *testing.T) {
	if !IsSignInProblem(Classify(errors.New("pcloud error: x (2095)"))) || !IsSignInProblem(Classify(errors.New("pcloud error: x (2094)"))) {
		t.Error("2094/2095 must count as sign-in problems")
	}
	if IsSignInProblem(Classify(errors.New("pcloud error: x (5000)"))) || IsSignInProblem(nil) {
		t.Error("a server problem or no error is not a sign-in problem")
	}
}

func TestNewApp(t *testing.T) {
	app, err := NewApp("  DnONSzyJXpm ", "\tAbCdEfGhIjKlMnOpQrStUvWxYz12\n")
	if err != nil || app.ClientID != "DnONSzyJXpm" || app.ClientSecret != "AbCdEfGhIjKlMnOpQrStUvWxYz12" || !app.IsSet() {
		t.Fatalf("got %+v, %v; want trimmed codes", app, err)
	}
	if app, err := NewApp(" ", ""); err != nil || app.IsSet() {
		t.Fatalf("empty codes: got %+v, %v; want rclone's app", app, err)
	}
	for _, bad := range [][2]string{
		{"DnONSzyJXpm", ""},             // secret missing
		{"", "AbCdEfGhIjKl"},            // ID missing
		{"--config=x", "AbCdEfGhIjKl"},  // would be read as an option
		{"Dn ON", "AbCdEfGhIjKl"},       // space inside
		{"DnONSzyJXpm", "abc\"; rm -x"}, // not a code at all
		{"abc", "AbCdEfGhIjKl"},         // too short
	} {
		if _, err := NewApp(bad[0], bad[1]); err == nil {
			t.Errorf("NewApp(%q, %q) accepted", bad[0], bad[1])
		}
	}
}

func TestAuthorizeCommandKeepsCodesOffTheCommandLine(t *testing.T) {
	app := App{ClientID: "OwnClientId", ClientSecret: "OwnClientSecret"}
	cmd := authorizeCommand(context.Background(), "rclone.exe", app)
	if line := strings.Join(cmd.Args, " "); strings.Contains(line, app.ClientID) || strings.Contains(line, app.ClientSecret) {
		t.Fatalf("codes on the command line: %s", line)
	}
	env := strings.Join(cmd.Env, "\n")
	for _, want := range []string{"RCLONE_PCLOUD_CLIENT_ID=OwnClientId", "RCLONE_PCLOUD_CLIENT_SECRET=OwnClientSecret"} {
		if !strings.Contains(env, want) {
			t.Errorf("environment lacks %s", want)
		}
	}
	if plain := authorizeCommand(context.Background(), "rclone.exe", App{}); plain.Env != nil {
		t.Error("without an own app the environment must be inherited unchanged")
	}
}
