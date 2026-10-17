// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// rclone's settings of a MEGA account (backend/mega, rclone 1.75.2). rclone
// signs in with the password once and then keeps the session and master key
// in the settings; with them it never needs the password or a two-factor
// code again.
const (
	megaUser      = "user"
	megaPassword  = "pass"
	megaCode      = "2fa"
	megaSessionID = "session_id"
	megaMasterKey = "master_key"
)

// logInRemote names the account in the rclone that only signs in.
const logInRemote = "cblogin"

// sessionMark starts the mark of the session that sessionOf adds to the
// e-mail address.
const sessionMark = " (session "

// maxUserLength is the longest possible e-mail address (RFC 5321).
const maxUserLength = 254

// codePattern: two-factor codes of authenticator apps have six digits.
var codePattern = regexp.MustCompile(`^[0-9]{6}$`)

// Credentials are what the user types to sign in to a service with password
// sign-in. They serve one sign-in and are never stored.
type Credentials struct {
	User     string `json:"user"`     // the e-mail address of the account
	Password string `json:"password"` // a secret
	Code     string `json:"code"`     // the current two-factor code; "" without two-factor authentication
}

// NewCredentials checks what the user typed. Spaces around the e-mail
// address and inside the code are ignored, as pasted text and codes shown
// as "123 456" bring them along; the password is kept exactly as typed.
func NewCredentials(user, password, code string) (Credentials, error) {
	creds := Credentials{
		User:     strings.TrimSpace(user),
		Password: password,
		Code:     strings.Join(strings.Fields(code), ""),
	}
	switch {
	case creds.User == "" || creds.Password == "":
		return Credentials{}, errors.New("enter the e-mail address and the password of the account")
	case len(creds.User) > maxUserLength || !strings.Contains(creds.User, "@") ||
		strings.ContainsFunc(creds.User, unicode.IsSpace):
		return Credentials{}, errors.New("the e-mail address does not look right")
	case creds.Code != "" && !codePattern.MatchString(creds.Code):
		return Credentials{}, errors.New("the two-factor code has six digits; leave it empty if the account has no two-factor authentication")
	}
	return creds, nil
}

// LogIn signs in to a service with password sign-in and returns the
// settings of the new session for Register. The password and the two-factor
// code are not among them.
//
// The sign-in runs in an rclone of its own that ends right after. An rclone
// signs in to a MEGA account only once while it runs and keeps that sign-in
// for every later remote of the same account (backend/mega, rclone 1.75.2):
// signing in again in the running rclone would give no new session.
func LogIn(ctx context.Context, rcloneExe string, service Service, creds Credentials) (map[string]string, error) {
	if service.SignIn != SignInPassword {
		return nil, fmt.Errorf("%s signs in in the browser, not with a password", service.Title)
	}
	folder, err := os.MkdirTemp("", "cryptbridge-signin-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(folder)

	stateFile := filepath.Join(folder, "rcd.json")
	client, err := rcd.Start(ctx, rcd.Options{RcloneExe: rcloneExe, StateFile: stateFile, ConfigFile: "NUL"})
	if err != nil {
		return nil, fmt.Errorf("start the %s sign-in: %w", service.Title, err)
	}
	// Without this safety net the sign-in still works; only a crash of
	// CryptBridge during the sign-in could leave this rclone behind.
	if release, err := endWithThisProgram(client.PID()); err == nil {
		defer release()
	}
	defer func() { _ = rcd.Stop(context.WithoutCancel(ctx), client, stateFile) }()

	params, err := logInWith(ctx, client, service, creds)
	if err != nil {
		return nil, err
	}
	return sessionOf(service, creds.User, params)
}

// logInWith signs in through client and returns rclone's settings of the
// signed-in account.
func logInWith(ctx context.Context, client *rcd.Client, service Service, creds Credentials) (map[string]string, error) {
	parameters := map[string]string{megaUser: creds.User, megaPassword: creds.Password}
	if creds.Code != "" {
		parameters[megaCode] = creds.Code
	}
	request := map[string]any{
		"name":       logInRemote,
		"type":       service.Type,
		"parameters": parameters,
		"opt":        map[string]any{"nonInteractive": true, "obscure": true}, // the password is as typed
	}
	// rclone asks nothing here (measured 2026-10-17): it only stores the
	// settings and signs in on the first use of the account.
	if err := client.Call(ctx, "config/create", request, nil); err != nil {
		return nil, fmt.Errorf("set up the %s sign-in: %w", service.Title, err)
	}
	if err := client.Call(ctx, "operations/about", map[string]any{"fs": logInRemote + ":"}, nil); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("the %s sign-in was cancelled", service.Title)
		}
		return nil, fmt.Errorf("the %s sign-in failed: %s", service.Title, explainLogInFailure(err.Error()))
	}
	return ReadParams(ctx, client, logInRemote)
}

// sessionOf returns the settings Register needs for the session in params:
// the e-mail address, the session and the master key.
//
// The e-mail address gets a mark of the session. rclone keeps each signed-in
// MEGA account under this name for as long as it runs and never signs in
// again for the same name, while a session needs no name to sign in with
// (backend/mega NewFs, rclone 1.75.2). Without the mark, a new session after
// MEGA ended the old one, e.g. after a password change, would only take
// effect once rclone restarts.
func sessionOf(service Service, user string, params map[string]string) (map[string]string, error) {
	sessionID, masterKey := params[megaSessionID], params[megaMasterKey]
	if sessionID == "" || masterKey == "" {
		return nil, fmt.Errorf("%s accepted the sign-in, but rclone kept no session", service.Title)
	}
	mark := sha256.Sum256([]byte(sessionID))
	return map[string]string{
		megaUser:      fmt.Sprintf("%s%s%x)", user, sessionMark, mark[:4]),
		megaSessionID: sessionID,
		megaMasterKey: masterKey,
	}, nil
}

// SessionUser returns the e-mail address an account with password sign-in
// was signed in with, from rclone's settings of the account.
func SessionUser(params map[string]string) string {
	user, _, _ := strings.Cut(params[megaUser], sessionMark)
	return user
}

// explainLogInFailure puts the reason of a failed sign-in into words where
// it is known. rclone passes on the texts of its MEGA library go-mega
// (errors.go, version c6acd6a5bd04 in rclone 1.75.2), which name MEGA's
// numbered errors.
func explainLogInFailure(message string) string {
	switch {
	case strings.Contains(message, "Object (typically, node or user) not found"): // -9
		return "the e-mail address or the password is wrong"
	case strings.Contains(message, "Multi-factor authentication required"): // -26
		return "the account has two-factor authentication: enter the current six-digit code of your authenticator app"
	case strings.Contains(message, "The upload failed"): // -5, which go-mega words for uploads only
		return "MEGA did not accept the two-factor code; enter the code your authenticator app shows right now"
	case strings.Contains(message, "User blocked"): // -16
		return "MEGA has blocked the account; sign in at mega.nz in the browser to see why"
	case strings.Contains(message, "Rate limit reached"), // -4
		strings.Contains(message, "Try again"),                          // -3
		strings.Contains(message, "Resource temporarily not available"): // -18
		return "MEGA asks to wait after many sign-ins in a short time; please try again later"
	}
	return message
}
