// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"context"
	"strings"
	"testing"
)

// driveAuthorizeOutput is what "rclone authorize drive" printed up to the
// sign-in (measured 2026-10-12), followed by the token block in the form
// rclone prints it for every service (measured with pCloud), token replaced.
const driveAuthorizeOutput = `2026/10/12 19:20:14 NOTICE: Make sure your Redirect URL is set to "http://127.0.0.1:53682/" in your custom config.
2026/10/12 19:20:14 NOTICE: Please go to the following link: http://127.0.0.1:53682/auth?state=DsjnjX9rjNSBzl8dgXVgaw
2026/10/12 19:20:14 NOTICE: Log in and authorize rclone for access
2026/10/12 19:20:14 NOTICE: Waiting for code...
2026/10/12 19:21:02 NOTICE: Got code
Paste the following into your remote machine --->
{"access_token":"TESTTOKEN","token_type":"Bearer","refresh_token":"TESTREFRESH","expiry":"2026-10-12T20:21:01+02:00","expires_in":3599}
<---End paste
`

func TestScanAuthorizeOutput(t *testing.T) {
	var links []string
	result, err := scanAuthorizeOutput(strings.NewReader(driveAuthorizeOutput), func(l string) { links = append(links, l) })
	if err != nil {
		t.Fatal(err)
	}
	token := result.token
	if result.failure != "" || !strings.HasPrefix(token, `{"access_token":"TESTTOKEN"`) || !strings.HasSuffix(token, `"expires_in":3599}`) {
		t.Fatalf("result = %+v", result)
	}
	if len(links) != 1 || links[0] != "http://127.0.0.1:53682/auth?state=DsjnjX9rjNSBzl8dgXVgaw" {
		t.Fatalf("links = %v", links)
	}
}

func TestScanAuthorizeOutputWithoutToken(t *testing.T) {
	result, err := scanAuthorizeOutput(strings.NewReader("NOTICE: Waiting for code...\n"), nil)
	if err != nil || result != (authorizeResult{}) {
		t.Fatalf("got %+v, %v; want nothing", result, err)
	}
}

// portBusyOutput is what "rclone authorize onedrive" printed while another
// sign-in held the port (measured 2026-10-12 on a German Windows), shortened.
const portBusyOutput = `2026/10/12 21:27:20 NOTICE: Make sure your Redirect URL is set to "http://localhost:53682/" in your custom config.
Error: config failed to refresh token: failed to start auth webserver: listen tcp 127.0.0.1:53682: bind: Normalerweise darf jede Socketadresse nur jeweils einmal verwendet werden.
Usage:
  rclone authorize <backendname> [base64_json_blob | client_id client_secret] [flags]

2026/10/12 21:27:20 NOTICE: Fatal error: config failed to refresh token: failed to start auth webserver: listen tcp 127.0.0.1:53682: bind: Normalerweise darf jede Socketadresse nur jeweils einmal verwendet werden.
`

func TestScanAuthorizeOutputKeepsRclonesReason(t *testing.T) {
	result, err := scanAuthorizeOutput(strings.NewReader(portBusyOutput), nil)
	if err != nil || result.token != "" || !strings.HasPrefix(result.failure, "config failed to refresh token: failed to start auth webserver") {
		t.Fatalf("got %+v, %v", result, err)
	}
	if text := explainAuthorizeFailure(result.failure); !strings.Contains(text, "53682") || !strings.Contains(text, "another sign-in") {
		t.Fatalf("explanation %q", text)
	}
	if other := explainAuthorizeFailure("something else"); other != "something else" {
		t.Fatalf("an unknown message changed to %q", other)
	}
}

func TestAuthorizeCommandKeepsCodesOffTheCommandLine(t *testing.T) {
	app := App{ClientID: "OwnClientId", ClientSecret: "OwnClientSecret"}
	for _, serviceType := range []string{PCloud, Drive, Dropbox} {
		service, err := ServiceOf(serviceType)
		if err != nil {
			t.Fatal(err)
		}
		cmd := authorizeCommand(context.Background(), "rclone.exe", service, app)
		if line := strings.Join(cmd.Args, " "); strings.Contains(line, app.ClientID) || strings.Contains(line, app.ClientSecret) {
			t.Fatalf("codes on the command line: %s", line)
		}
		if got := cmd.Args[len(cmd.Args)-1]; got != serviceType {
			t.Errorf("authorize %q, want %q", got, serviceType)
		}
		prefix := "RCLONE_" + strings.ToUpper(serviceType) + "_"
		env := strings.Join(cmd.Env, "\n")
		for _, want := range []string{prefix + "CLIENT_ID=OwnClientId", prefix + "CLIENT_SECRET=OwnClientSecret"} {
			if !strings.Contains(env, want) {
				t.Errorf("environment lacks %s", want)
			}
		}
		if plain := authorizeCommand(context.Background(), "rclone.exe", service, App{}); plain.Env != nil {
			t.Error("without an own app the environment must be inherited unchanged")
		}
	}
}
