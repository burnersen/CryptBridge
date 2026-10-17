// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"errors"
	"strings"
	"testing"

	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		service string
		message string
		want    error
	}{
		// Measured 2026-10-12 (rclone 1.75.1, operations/about): renewal of an
		// expired token refused, and a token the service cannot read.
		{Drive, `couldn't find root directory ID: Get "https://www.googleapis.com/drive/v3/files/root?alt=json": couldn't fetch token: invalid_client: if you're using your own client id/secret, make sure they're properly set up following the docs`, ErrAppRejected},
		{Drive, "couldn't find root directory ID: googleapi: Error 401: Request had invalid authentication credentials. Expected OAuth 2 access token, login cookie or other valid authentication credential.", ErrSignInExpired},
		{OneDrive, `failed to get root: Get "https://graph.microsoft.com/v1.0/drives/abc123/root": couldn't fetch token: invalid_grant: maybe token expired? - try refreshing with "rclone config reconnect eonedrive:"`, ErrSignInExpired},
		{OneDrive, "failed to get root: InvalidAuthenticationToken: Protocol 'Bearer' failed to validate because The token could not be read.", ErrSignInExpired},
		{Dropbox, `about call failed: Post "https://api.dropboxapi.com/2/users/get_space_usage": couldn't fetch token: invalid_grant: maybe token expired? - try refreshing with "rclone config reconnect edropbox:"`, ErrSignInExpired},
		{Dropbox, "about call failed: invalid_access_token/", ErrSignInExpired},
		// Built like Go's network errors (measured offline with pCloud): a
		// renewal without network is not a refused sign-in.
		{Drive, `couldn't fetch token: Post "https://oauth2.googleapis.com/token": dial tcp: lookup oauth2.googleapis.com: no such host`, ErrUnreachable},
		// Built from rclone 1.75.2's and go-mega's texts (not measured).
		{Mega, "login with previous auth keys failed: Invalid or expired user session, please relogin", ErrSignInExpired},
		{Mega, "couldn't decode master key: illegal base64 data at input byte 4", ErrSignInExpired},
		{Mega, `login with previous auth keys failed: Post "https://g.api.mega.co.nz/cs?id=1": dial tcp: lookup g.api.mega.co.nz: no such host`, ErrUnreachable},
	}
	for _, c := range cases {
		original := errors.New(c.message)
		got := Classify(mustService(t, c.service), original)
		if !errors.Is(got, c.want) {
			t.Errorf("%s %q: got %v, want %v", c.service, c.message, got, c.want)
			continue
		}
		if !errors.Is(got, original) {
			t.Errorf("%q: rclone's error got lost", c.message)
		}
		if title := mustService(t, c.service).Title; !strings.HasPrefix(got.Error(), title+" ") {
			t.Errorf("%q: message %q does not name %s", c.message, got.Error(), title)
		}
	}
}

func TestClassifyLeavesOtherErrorsAlone(t *testing.T) {
	drive := mustService(t, Drive)
	original := errors.New("directory not found")
	if got := Classify(drive, original); got != original {
		t.Errorf("changed to %v", got)
	}
	if Classify(drive, nil) != nil {
		t.Error("nil must stay nil")
	}
	once := Classify(drive, errors.New("x: invalid_grant"))
	if Classify(drive, once) != once {
		t.Error("classifying twice must not wrap again")
	}
}

func TestClassifyUsesPCloudsNumbers(t *testing.T) {
	got := Classify(mustService(t, PCloud), errors.New("about call failed: pcloud error: Invalid 'access_token' provided. (2094)"))
	if !errors.Is(got, pcloud.ErrSignInRejected) || !IsSignInProblem(got) {
		t.Fatalf("got %v, want pCloud's sign-in problem", got)
	}
}

func TestIsSignInProblem(t *testing.T) {
	drive := mustService(t, Drive)
	for _, message := range []string{"x: invalid_grant", "x: invalid_client"} {
		if !IsSignInProblem(Classify(drive, errors.New(message))) {
			t.Errorf("%q must count as a sign-in problem", message)
		}
	}
	if IsSignInProblem(Classify(drive, errors.New("dial tcp: no such host"))) || IsSignInProblem(nil) {
		t.Error("no network or no error is not a sign-in problem")
	}
}

func TestAdviceForGoogleNamesTheTestingMode(t *testing.T) {
	drive := mustService(t, Drive)
	advice := Advice(drive, Classify(drive, errors.New("x: invalid_grant")))
	if !strings.Contains(advice, "Testing") || !strings.HasPrefix(advice, "Google Drive no longer accepts") {
		t.Errorf("advice = %q", advice)
	}
	dropbox := mustService(t, Dropbox)
	if advice := Advice(dropbox, Classify(dropbox, errors.New("x: invalid_grant"))); !strings.HasSuffix(advice, "Please sign in again.") {
		t.Errorf("advice = %q", advice)
	}
}
