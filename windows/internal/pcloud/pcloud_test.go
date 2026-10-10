// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package pcloud

import (
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
	revoked := errors.New("about call failed: pcloud error: Revoked 'access_token' provided, you need to re-authorize the application. (2095)")
	if !errors.Is(Classify(revoked), ErrSignInRevoked) {
		t.Error("2095 not classified as revoked")
	}
	wrongRegion := errors.New("about call failed: pcloud error: Invalid 'access_token' provided. (2094)")
	if !errors.Is(Classify(wrongRegion), ErrSignInRejected) {
		t.Error("2094 not classified as rejected")
	}
	other := errors.New("connection refused")
	if Classify(other) != other {
		t.Error("unrelated error must pass through unchanged")
	}
	if Classify(nil) != nil {
		t.Error("nil must stay nil")
	}
}
