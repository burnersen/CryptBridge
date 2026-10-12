// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// tokenLine matches the token rclone prints after a successful sign-in.
var tokenLine = regexp.MustCompile(`^\{"access_token":.*\}$`)

// authURL matches the link rclone prints in case the browser does not open.
var authURL = regexp.MustCompile(`http://127\.0\.0\.1:\d+/auth\?state=\S+`)

// Authorize runs "rclone authorize" for the service: rclone opens the
// service's sign-in page in the default browser and waits until the user has
// signed in. With an own app, the sign-in goes through that app instead of
// rclone's. onLink receives the sign-in link as soon as rclone prints it, so
// that it can be shown in case the browser does not open by itself.
// Cancelling ctx stops the sign-in.
func Authorize(ctx context.Context, rcloneExe string, service Service, app App, onLink func(string)) (string, error) {
	cmd := authorizeCommand(ctx, rcloneExe, service, app)
	output, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = cmd.Stdout // rclone prints the link on stderr, the token on stdout
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start the %s sign-in: %w", service.Title, err)
	}
	// Without this safety net the sign-in still works; only a crash of
	// CryptBridge during the sign-in could leave rclone behind.
	if release, err := endWithThisProgram(cmd.Process.Pid); err == nil {
		defer release()
	}

	result, scanErr := scanAuthorizeOutput(output, onLink)
	waitErr := cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return "", fmt.Errorf("the %s sign-in was cancelled", service.Title)
	case result.token != "":
		return result.token, nil
	case scanErr != nil:
		return "", fmt.Errorf("read the %s sign-in: %w", service.Title, scanErr)
	case result.failure != "":
		return "", fmt.Errorf("the %s sign-in failed: %s", service.Title, explainAuthorizeFailure(result.failure))
	case waitErr != nil:
		return "", fmt.Errorf("the %s sign-in failed: %w", service.Title, waitErr)
	}
	return "", errors.New("the sign-in ended without a token")
}

// authorizeResult is what rclone printed: the token, or why it failed.
type authorizeResult struct {
	token   string
	failure string // rclone's own error message
}

// failureLine matches rclone's last words when the sign-in fails, e.g.
// "2026/10/12 21:27:20 NOTICE: Fatal error: config failed to refresh token:
// failed to start auth webserver: listen tcp 127.0.0.1:53682: bind: …"
// (measured with rclone 1.75.1).
var failureLine = regexp.MustCompile(`Fatal error: (.+)$`)

// explainAuthorizeFailure puts rclone's message into words where the cause
// is known.
func explainAuthorizeFailure(message string) string {
	if strings.Contains(message, "failed to start auth webserver") {
		return "the port 53682 that receives the sign-in is in use — another sign-in is still waiting " +
			"(maybe in an rclone window). Finish or close it, then try again"
	}
	return message
}

// authorizeCommand builds "rclone authorize <type>". The codes of an own app
// go through the environment, not the command line, because security
// software often records command lines. rclone 1.75.1 reads them from
// RCLONE_<TYPE>_CLIENT_ID and _SECRET (measured for pCloud, Google Drive and
// Dropbox: the sign-in page was asked for with the own client ID).
func authorizeCommand(ctx context.Context, rcloneExe string, service Service, app App) *exec.Cmd {
	cmd := exec.CommandContext(ctx, rcloneExe, "authorize", service.Type)
	cmd.SysProcAttr = hiddenWindow()
	if app.IsSet() {
		prefix := "RCLONE_" + strings.ToUpper(service.Type) + "_"
		cmd.Env = append(os.Environ(),
			prefix+"CLIENT_ID="+app.ClientID,
			prefix+"CLIENT_SECRET="+app.ClientSecret)
	}
	return cmd
}

// scanAuthorizeOutput reads rclone's output line by line, reports the link
// once and returns the token line, or rclone's message if it failed.
func scanAuthorizeOutput(r io.Reader, onLink func(string)) (authorizeResult, error) {
	var once sync.Once
	var result authorizeResult
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if link := authURL.FindString(line); link != "" && onLink != nil {
			once.Do(func() { onLink(link) })
		}
		if tokenLine.MatchString(line) {
			result.token = line
		}
		if match := failureLine.FindStringSubmatch(line); match != nil {
			result.failure = match[1]
		}
	}
	return result, scanner.Err()
}
