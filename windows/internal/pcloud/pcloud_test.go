// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package pcloud

import (
	"errors"
	"testing"
)

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
