// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package pcloud signs in to pCloud through rclone and registers the account
// with the running rclone process.
package pcloud

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"

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

var (
	// ErrSignInRevoked: pCloud error 2095, e.g. after a password change or
	// "log out of all devices". Only a new sign-in helps.
	ErrSignInRevoked = errors.New("pCloud has revoked the sign-in; please sign in again")

	// ErrSignInRejected: pCloud error 2094 on both servers.
	ErrSignInRejected = errors.New("pCloud does not accept the sign-in; please sign in again")
)

// Account is a signed-in pCloud account.
type Account struct {
	Hostname string `json:"hostname"`
	Token    string `json:"token"` // rclone's token JSON; a secret
}

// Usage is the storage of the account in bytes.
type Usage struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
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

// ReadUsage asks pCloud how much storage is used and free.
func ReadUsage(ctx context.Context, client *rcd.Client, remote string) (Usage, error) {
	var usage Usage
	if err := client.Call(ctx, "operations/about", map[string]any{"fs": remote}, &usage); err != nil {
		return usage, Classify(err)
	}
	return usage, nil
}

// FindRegion returns the API server that accepts the token. The answer of
// the wrong server is "Invalid 'access_token' provided. (2094)" (measured).
func FindRegion(ctx context.Context, client *rcd.Client, token string) (string, error) {
	defer client.Call(context.WithoutCancel(ctx), "config/delete", map[string]any{"name": regionProbeRemote}, nil)
	for _, host := range []string{HostEU, HostUS} {
		if err := Configure(ctx, client, regionProbeRemote, Account{Hostname: host, Token: token}); err != nil {
			return "", err
		}
		_, err := ReadUsage(ctx, client, regionProbeRemote+":")
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

// Classify turns pCloud's numbered errors into the errors of this package.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "(2095)"):
		return fmt.Errorf("%w (%v)", ErrSignInRevoked, err)
	case strings.Contains(message, "(2094)"):
		return fmt.Errorf("%w (%v)", ErrSignInRejected, err)
	}
	return err
}

// tokenLine matches the token rclone prints after a successful sign-in.
var tokenLine = regexp.MustCompile(`^\{"access_token":.*\}$`)

// authURL matches the link rclone prints in case the browser does not open.
var authURL = regexp.MustCompile(`http://127\.0\.0\.1:\d+/auth\?state=\S+`)

// Authorize runs "rclone authorize pcloud": rclone opens the pCloud sign-in
// page in the default browser and waits until the user has signed in.
// onLink receives the sign-in link as soon as rclone prints it, so that it
// can be shown in case the browser does not open by itself. Cancelling ctx
// stops the sign-in.
func Authorize(ctx context.Context, rcloneExe string, onLink func(string)) (string, error) {
	cmd := exec.CommandContext(ctx, rcloneExe, "authorize", "pcloud")
	cmd.SysProcAttr = hiddenWindow()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = cmd.Stdout // rclone prints the link on stderr, the token on stdout
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start pCloud sign-in: %w", err)
	}

	token, scanErr := scanAuthorizeOutput(output, onLink)
	waitErr := cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return "", errors.New("pCloud sign-in was cancelled")
	case token != "":
		return token, nil
	case scanErr != nil:
		return "", fmt.Errorf("read pCloud sign-in: %w", scanErr)
	case waitErr != nil:
		return "", fmt.Errorf("pCloud sign-in failed: %w", waitErr)
	}
	return "", errors.New("pCloud sign-in ended without a token")
}

// scanAuthorizeOutput reads rclone's output line by line, reports the link
// once and returns the token line.
func scanAuthorizeOutput(r io.Reader, onLink func(string)) (string, error) {
	var once sync.Once
	token := ""
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if link := authURL.FindString(line); link != "" && onLink != nil {
			once.Do(func() { onLink(link) })
		}
		if tokenLine.MatchString(line) {
			token = line
		}
	}
	return token, scanner.Err()
}
