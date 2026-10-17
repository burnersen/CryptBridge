// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/testrclone"
)

// These tests use a real rclone (from PATH) with a made-up token. They reach
// no cloud service: the questions they go through are asked without one
// (measured 2026-10-12).

const fakeToken = `{"access_token":"fake","token_type":"Bearer","refresh_token":"fake","expiry":"2030-01-01T00:00:00Z"}`

func startRclone(t *testing.T) (context.Context, *rcd.Client) {
	t.Helper()
	exe := testrclone.Path(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	stateFile := filepath.Join(t.TempDir(), "rcd.json")
	client, err := rcd.Start(ctx, rcd.Options{RcloneExe: exe, StateFile: stateFile, ConfigFile: "NUL"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rcd.Stop(context.Background(), client, stateFile) })
	return ctx, client
}

func remotes(t *testing.T, ctx context.Context, client *rcd.Client) []string {
	t.Helper()
	var answer struct {
		Remotes []string `json:"remotes"`
	}
	if err := client.Call(ctx, "config/listremotes", nil, &answer); err != nil {
		t.Fatal(err)
	}
	return answer.Remotes
}

func noQuestions(_ context.Context, q Question) (string, error) {
	return "", errors.New("unexpected question " + q.Name)
}

func TestRegisterDropboxAndRestore(t *testing.T) {
	ctx, client := startRclone(t)
	params, err := Register(ctx, client, "cbtest", mustService(t, Dropbox), fakeToken, App{}, noQuestions)
	if err != nil {
		t.Fatal(err)
	}
	if params["type"] != Dropbox || params["token"] != fakeToken || params["batch_mode"] != "off" {
		t.Fatalf("params = %v", params)
	}

	removeRemote(ctx, client, "cbtest")
	if err := Restore(ctx, client, "cbtest", params); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadParams(ctx, client, "cbtest")
	if err != nil {
		t.Fatal(err)
	}
	if restored["type"] != Dropbox || restored["token"] != fakeToken || restored["batch_mode"] != "off" {
		t.Fatalf("restored = %v", restored)
	}
}

// A Dropbox account stored before CryptBridge turned batching off gets it
// when it is registered again (see Service.fixed).
func TestRestoreAddsFixedSettings(t *testing.T) {
	ctx, client := startRclone(t)
	old := map[string]string{"type": Dropbox, "token": fakeToken}
	if err := Restore(ctx, client, "cbtest", old); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadParams(ctx, client, "cbtest")
	if err != nil {
		t.Fatal(err)
	}
	if restored["batch_mode"] != "off" {
		t.Fatalf("restored = %v; want batching off", restored)
	}
	if _, changed := old["batch_mode"]; changed {
		t.Fatal("Restore changed the caller's settings")
	}
}

func TestRegisterGoogleDriveWithOwnApp(t *testing.T) {
	ctx, client := startRclone(t)
	app := App{ClientID: "123-abc.apps.googleusercontent.com", ClientSecret: "GOCSPX-test"}
	params, err := Register(ctx, client, "cbtest", mustService(t, Drive), fakeToken, app, noQuestions)
	if err != nil {
		t.Fatal(err)
	}
	// The secret must be kept as typed: Restore hands it back unchanged.
	if params["client_id"] != app.ClientID || params["client_secret"] != app.ClientSecret || params["token"] != fakeToken {
		t.Fatalf("params = %v", params)
	}
}

func TestRegisterAsksTheUserWhatItCannotAnswer(t *testing.T) {
	ctx, client := startRclone(t)
	service := mustService(t, Drive)
	service.answers = nil // leave "Configure this as a Shared Drive?" to the user
	var asked []Question
	ask := func(_ context.Context, q Question) (string, error) {
		asked = append(asked, q)
		return "false", nil
	}
	app := App{ClientID: "123-abc.apps.googleusercontent.com", ClientSecret: "GOCSPX-test"}
	if _, err := Register(ctx, client, "cbtest", service, fakeToken, app, ask); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0].Name != "config_change_team_drive" || len(asked[0].Choices) != 2 {
		t.Fatalf("asked = %+v", asked)
	}
}

func TestRegisterRemovesTheRemoteWhenCancelled(t *testing.T) {
	ctx, client := startRclone(t)
	service := mustService(t, Drive)
	service.answers = nil
	cancelled := errors.New("cancelled")
	ask := func(context.Context, Question) (string, error) { return "", cancelled }
	app := App{ClientID: "123-abc.apps.googleusercontent.com", ClientSecret: "GOCSPX-test"}
	if _, err := Register(ctx, client, "cbtest", service, fakeToken, app, ask); !errors.Is(err, cancelled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if slices.Contains(remotes(t, ctx, client), "cbtest") {
		t.Fatal("the half set-up remote is still there")
	}
}

// rclone stores a MEGA account without signing in (measured 2026-10-17), so
// a made-up session reaches no service either.
func TestRegisterMegaSessionAndRestore(t *testing.T) {
	ctx, client := startRclone(t)
	session := map[string]string{
		megaUser:      "name@example.com (session 01020304)",
		megaSessionID: "fake-session",
		megaMasterKey: "a2V5",
	}
	params, err := RegisterSession(ctx, client, "cbtest", mustService(t, Mega), session)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range session {
		if params[key] != value {
			t.Fatalf("params = %v; want %s = %q", params, key, value)
		}
	}
	if params["type"] != Mega || params[megaPassword] != "" || params[megaCode] != "" || !HasSignIn(params) {
		t.Fatalf("params = %v", params)
	}

	removeRemote(ctx, client, "cbtest")
	if err := Restore(ctx, client, "cbtest", params); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadParams(ctx, client, "cbtest")
	if err != nil {
		t.Fatal(err)
	}
	if restored[megaSessionID] != "fake-session" || restored[megaMasterKey] != "a2V5" || restored[megaUser] != session[megaUser] {
		t.Fatalf("restored = %v", restored)
	}
}

func TestRestoreNeedsASignIn(t *testing.T) {
	ctx, client := startRclone(t)
	if err := Restore(ctx, client, "cbtest", map[string]string{"type": Dropbox}); err == nil {
		t.Fatal("restored an account without token")
	}
	if err := Restore(ctx, client, "cbtest", map[string]string{"type": Mega, megaUser: "name@example.com"}); err == nil {
		t.Fatal("restored a MEGA account without session")
	}
	if _, err := ReadParams(ctx, client, "cbmissing"); err == nil {
		t.Fatal("read the settings of a missing remote")
	}
}

func TestOnlyChoice(t *testing.T) {
	one := Question{Choices: []Choice{{Value: "b!123"}}}
	if reply, ok := onlyChoice(one); !ok || reply != "b!123" {
		t.Errorf("one choice: got %q, %v", reply, ok)
	}
	two := Question{Choices: []Choice{{Value: "a"}, {Value: "b"}}}
	if _, ok := onlyChoice(two); ok {
		t.Error("with two choices the user must choose")
	}
}
