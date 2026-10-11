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
	"os"
	"os/exec"
	"regexp"
	"strconv"
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

// App is the user's own pCloud app ("My Apps" on docs.pcloud.com). Without
// one, rclone's built-in app is used, which pCloud has blocked for hours
// before (2021-07-28, 2026-10-05).
//
// The app is only needed for signing in: pCloud's sign-ins never expire and
// have nothing to refresh, so the remotes themselves need only the token.
type App struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"` // a secret
}

// appCodePattern: rclone's own codes are 11 and 28 letters and digits
// (measured). "_" and "-" are accepted to be safe, but never a leading "-",
// which rclone would read as an option.
var appCodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{3,127}$`)

// NewApp checks the codes of an own pCloud app, ignoring spaces around
// pasted text. Two empty codes are fine: they mean "use rclone's app".
func NewApp(clientID, clientSecret string) (App, error) {
	app := App{ClientID: strings.TrimSpace(clientID), ClientSecret: strings.TrimSpace(clientSecret)}
	switch {
	case app.ClientID == "" && app.ClientSecret == "":
		return App{}, nil
	case app.ClientID == "" || app.ClientSecret == "":
		return App{}, errors.New("enter both the client ID and the client secret of your pCloud app")
	case !appCodePattern.MatchString(app.ClientID):
		return App{}, errors.New("the client ID does not look right; copy it again from your app on pCloud")
	case !appCodePattern.MatchString(app.ClientSecret):
		return App{}, errors.New("the client secret does not look right; copy it again from your app on pCloud")
	}
	return app, nil
}

// IsSet reports whether an own app is configured.
func (a App) IsSet() bool {
	return a.ClientID != ""
}

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

// tokenLine matches the token rclone prints after a successful sign-in.
var tokenLine = regexp.MustCompile(`^\{"access_token":.*\}$`)

// authURL matches the link rclone prints in case the browser does not open.
var authURL = regexp.MustCompile(`http://127\.0\.0\.1:\d+/auth\?state=\S+`)

// Authorize runs "rclone authorize pcloud": rclone opens the pCloud sign-in
// page in the default browser and waits until the user has signed in.
// With an own app, the sign-in goes through that app instead of rclone's.
// onLink receives the sign-in link as soon as rclone prints it, so that it
// can be shown in case the browser does not open by itself. Cancelling ctx
// stops the sign-in.
func Authorize(ctx context.Context, rcloneExe string, app App, onLink func(string)) (string, error) {
	cmd := authorizeCommand(ctx, rcloneExe, app)
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

// authorizeCommand builds "rclone authorize pcloud". The codes of an own app
// go through the environment, not the command line, because security
// software often records command lines (rclone 1.75.1 reads them from the
// environment, measured). The last value wins if the user has set them too.
func authorizeCommand(ctx context.Context, rcloneExe string, app App) *exec.Cmd {
	cmd := exec.CommandContext(ctx, rcloneExe, "authorize", "pcloud")
	cmd.SysProcAttr = hiddenWindow()
	if app.IsSet() {
		cmd.Env = append(os.Environ(),
			"RCLONE_PCLOUD_CLIENT_ID="+app.ClientID,
			"RCLONE_PCLOUD_CLIENT_SECRET="+app.ClientSecret)
	}
	return cmd
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
