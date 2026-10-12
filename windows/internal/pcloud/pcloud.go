// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package pcloud knows what is special about pCloud: its two regions, whose
// servers accept only their own sign-ins, and its numbered errors. Signing
// in works as for every service (see package cloud).
package pcloud

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// pCloud keeps accounts in two regions with separate API servers. A sign-in
// only works against the server of its own region.
const (
	HostEU = "eapi.pcloud.com"
	HostUS = "api.pcloud.com"
)

// regionProbeRemote is a temporary remote used while finding the region.
const regionProbeRemote = "cbregion"

// RedirectURL is where rclone receives the sign-in. An own pCloud app must
// list it under "Redirect URIs" (measured with rclone 1.75.1).
const RedirectURL = "http://localhost:53682/"

var (
	// ErrSignInRevoked: pCloud error 2095. It follows a password change or
	// "log out of all devices" — but pCloud also answered it for hours while
	// it was blocking rclone's built-in app (2026-10-05), and the very same
	// sign-ins worked again afterwards. So it is not always final.
	ErrSignInRevoked = errors.New("pCloud no longer accepts the sign-in (error 2095)")

	// ErrSignInRejected: pCloud error 2094, also the answer of the wrong
	// region's server.
	ErrSignInRejected = errors.New("pCloud does not accept the sign-in (error 2094)")

	// ErrBusy: pCloud errors 4xxx, "too many requests or login tries".
	ErrBusy = errors.New("pCloud is limiting requests right now; it works again by itself shortly")

	// ErrServerProblem: pCloud errors 5xxx, a failure on pCloud's side.
	ErrServerProblem = errors.New("pCloud has a server problem right now; it usually works again by itself later")

	// ErrUnreachable: no network connection to pCloud.
	ErrUnreachable = errors.New("pCloud cannot be reached; please check the internet connection")
)

// Problem is a pCloud error explained in plain words. Error() returns only
// the explanation; errors.Is finds both the kind and rclone's original error.
type Problem struct {
	Kind  error // one of the Err… values above
	Cause error // rclone's original error
}

func (p *Problem) Error() string   { return p.Kind.Error() }
func (p *Problem) Unwrap() []error { return []error{p.Kind, p.Cause} }

// IsSignInProblem reports whether pCloud refused the sign-in itself, as
// opposed to a passing problem with the network or pCloud's servers.
func IsSignInProblem(err error) bool {
	return errors.Is(err, ErrSignInRevoked) || errors.Is(err, ErrSignInRejected)
}

// Account is a signed-in pCloud account, in the form CryptBridge 0.6 stored
// it and the Linux setup takes it.
type Account struct {
	Hostname string `json:"hostname"`
	Token    string `json:"token"` // rclone's token JSON; a secret
}

// Configure registers the account in rclone's in-memory configuration.
func Configure(ctx context.Context, client *rcd.Client, name string, account Account) error {
	if account.Token == "" || account.Hostname == "" {
		return errors.New("not signed in to pCloud")
	}
	_ = client.Call(ctx, "config/delete", map[string]any{"name": name}, nil)
	request := map[string]any{
		"name":       name,
		"type":       "pcloud",
		"parameters": map[string]string{"hostname": account.Hostname, "token": account.Token},
		"opt":        map[string]any{"nonInteractive": true},
	}
	if err := client.Call(ctx, "config/create", request, nil); err != nil {
		return fmt.Errorf("register pCloud account: %w", err)
	}
	return nil
}

// FindRegion returns the API server that accepts the token. The answer of
// the wrong server is "Invalid 'access_token' provided. (2094)" (measured).
func FindRegion(ctx context.Context, client *rcd.Client, token string) (string, error) {
	defer client.Call(context.WithoutCancel(ctx), "config/delete", map[string]any{"name": regionProbeRemote}, nil)
	for _, host := range []string{HostEU, HostUS} {
		if err := Configure(ctx, client, regionProbeRemote, Account{Hostname: host, Token: token}); err != nil {
			return "", err
		}
		err := Classify(client.Call(ctx, "operations/about", map[string]any{"fs": regionProbeRemote + ":"}, nil))
		switch {
		case err == nil:
			return host, nil
		case errors.Is(err, ErrSignInRejected):
			continue
		default:
			return "", err
		}
	}
	return "", ErrSignInRejected
}

// errorCode finds the number at the end of pCloud's error messages, e.g.
// "pcloud error: Invalid 'access_token' provided. (2094)" (measured).
var errorCode = regexp.MustCompile(`pcloud error: .*\((\d{4})\)`)

// unreachableHints are parts of Go's network errors. Measured offline:
// `dial tcp: lookup eapi.pcloud.com: no such host`.
var unreachableHints = []string{"dial tcp", "i/o timeout", "TLS handshake timeout"}

// Classify explains pCloud's numbered errors and network failures as a
// *Problem; any other error is returned unchanged.
func Classify(err error) error {
	var problem *Problem
	if err == nil || errors.As(err, &problem) {
		return err
	}
	if kind := kindOf(err.Error()); kind != nil {
		return &Problem{Kind: kind, Cause: err}
	}
	return err
}

// kindOf sorts an error message into one of the Err… values, or nil.
// pCloud groups its error numbers by thousands (docs.pcloud.com/errors).
func kindOf(message string) error {
	if match := errorCode.FindStringSubmatch(message); match != nil {
		code, _ := strconv.Atoi(match[1]) // the pattern allows digits only
		switch {
		case code == 2095:
			return ErrSignInRevoked
		case code == 2094:
			return ErrSignInRejected
		case code/1000 == 4:
			return ErrBusy
		case code/1000 == 5:
			return ErrServerProblem
		}
		return nil
	}
	for _, hint := range unreachableHints {
		if strings.Contains(message, hint) {
			return ErrUnreachable
		}
	}
	return nil
}
