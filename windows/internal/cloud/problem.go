// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"context"
	"errors"
	"strings"

	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// The kinds of problems with a service that are explained in plain words.
// Their texts follow the service's name (see Problem.Error).
var (
	// ErrSignInExpired: the service refuses the sign-in. It ran out (Google
	// ends it after 7 days while an own app is still in testing), or it was
	// withdrawn, e.g. by a password change.
	ErrSignInExpired = errors.New("no longer accepts the sign-in: it has run out or was withdrawn")

	// ErrAppRejected: the service does not know the own app's client ID or
	// secret, e.g. because the app was deleted or a code was mistyped.
	ErrAppRejected = errors.New("does not accept your own app: check its client ID and client secret")

	// ErrUnreachable: no network connection to the service.
	ErrUnreachable = errors.New("cannot be reached; please check the internet connection")
)

// Problem is an error of a service explained in plain words. Error() returns
// only the explanation; errors.Is finds both the kind and rclone's original
// error.
type Problem struct {
	Service string // title, e.g. "Google Drive"
	Kind    error  // one of the Err… values above
	Cause   error  // rclone's original error
}

func (p *Problem) Error() string   { return p.Service + " " + p.Kind.Error() }
func (p *Problem) Unwrap() []error { return []error{p.Kind, p.Cause} }

// Parts of rclone's error messages, measured 2026-10-12 with rclone 1.75.1
// against Google Drive, OneDrive and Dropbox (see problem_test.go).
var (
	// unreachableHints come first: a token renewal can fail for want of a
	// network, and that is not a refused sign-in.
	unreachableHints = []string{"dial tcp", "i/o timeout", "TLS handshake timeout"}
	appRejectedHints = []string{"invalid_client", "unauthorized_client"}
	expiredHints     = []string{
		"invalid_grant",              // renewal refused (all OAuth services)
		"Error 401",                  // Google Drive: invalid credentials
		"InvalidAuthenticationToken", // OneDrive
		"invalid_access_token",       // Dropbox
		"expired_access_token",       // Dropbox
		// MEGA ended the session, e.g. after a password change (go-mega's
		// text for MEGA's error -15; not measured).
		"Invalid or expired user session",
		// The stored session cannot be read (rclone 1.75.2, backend/mega).
		"couldn't decode master key",
	}
)

// Classify explains the errors of a service that the user can act on as a
// *Problem; any other error is returned unchanged. pCloud has its own
// numbered errors (see package pcloud).
func Classify(service Service, err error) error {
	if err == nil {
		return nil
	}
	if service.Type == PCloud {
		return pcloud.Classify(err)
	}
	var problem *Problem
	if errors.As(err, &problem) {
		return err
	}
	if kind := kindOf(err.Error()); kind != nil {
		return &Problem{Service: service.Title, Kind: kind, Cause: err}
	}
	return err
}

// kindOf sorts an error message into one of the Err… values, or nil.
func kindOf(message string) error {
	switch {
	case containsAny(message, unreachableHints):
		return ErrUnreachable
	case containsAny(message, appRejectedHints):
		return ErrAppRejected
	case containsAny(message, expiredHints):
		return ErrSignInExpired
	}
	return nil
}

func containsAny(message string, parts []string) bool {
	for _, part := range parts {
		if strings.Contains(message, part) {
			return true
		}
	}
	return false
}

// IsSignInProblem reports whether a service refused the sign-in or the own
// app, as opposed to a passing problem with the network or the servers.
func IsSignInProblem(err error) bool {
	return errors.Is(err, ErrSignInExpired) || errors.Is(err, ErrAppRejected) || pcloud.IsSignInProblem(err)
}

// Advice says what a refused sign-in means and what helps; err comes from
// Classify.
func Advice(service Service, err error) string {
	switch {
	case errors.Is(err, pcloud.ErrSignInRevoked):
		return err.Error() + `. This follows a password change or "log out of all devices", but pCloud has also ` +
			"caused it during outages that ended by themselves. CryptBridge asks pCloud again every minute and " +
			"removes this message as soon as the sign-in works again. If it lasts, sign in again."
	case errors.Is(err, ErrAppRejected):
		return err.Error() + ". Correct them in the account's settings, then sign in again."
	case errors.Is(err, ErrSignInExpired) && service.Type == Drive:
		return err.Error() + `. If your Google app is still in "Testing", Google ends every sign-in after ` +
			`7 days: publish the app as the guide shows, then sign in again.`
	}
	return err.Error() + ". Please sign in again."
}

// Usage is the storage of an account in bytes. Total and Free are 0 where a
// service does not tell them.
type Usage struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

// ReadUsage asks the service how much storage is used and free. The answer
// also shows whether the service accepts the sign-in.
func ReadUsage(ctx context.Context, client *rcd.Client, service Service, remote string) (Usage, error) {
	var usage Usage
	if err := client.Call(ctx, "operations/about", map[string]any{"fs": remote}, &usage); err != nil {
		return usage, Classify(service, err)
	}
	return usage, nil
}
