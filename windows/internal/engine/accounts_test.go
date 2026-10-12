// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/cloud"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

func TestCheckAccountName(t *testing.T) {
	accounts := []settings.Account{{ID: "a1", Name: "Google Drive"}}
	for _, bad := range []string{"", "   ", "google drive", strings.Repeat("x", 41), "tab\there"} {
		if _, err := checkAccountName(bad, "", accounts); err == nil {
			t.Errorf("checkAccountName(%q) accepted", bad)
		}
	}
	if got, err := checkAccountName("  Google Drive ", "a1", accounts); err != nil || got != "Google Drive" {
		t.Errorf("keeping the own name: %q %v", got, err)
	}
	if got, err := checkAccountName("Work: OneDrive (Müller)", "", accounts); err != nil || got != "Work: OneDrive (Müller)" {
		t.Errorf("a good name: %q %v", got, err)
	}
}

func TestDefaultAccountName(t *testing.T) {
	drive, err := cloud.ServiceOf(cloud.Drive)
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultAccountName(drive, nil); got != "Google Drive" {
		t.Errorf("first account: %q", got)
	}
	taken := []settings.Account{{Name: "Google Drive"}, {Name: "google drive 2"}}
	if got := defaultAccountName(drive, taken); got != "Google Drive 3" {
		t.Errorf("third account: %q", got)
	}
}

func TestNewAccountID(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id, err := newAccountID(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(id, "a") || len(id) != 9 || seen[id] {
			t.Fatalf("ID %q: want a + 8 hex digits, never twice", id)
		}
		seen[id] = true
	}
}

// accountEngine returns an engine with the given settings in memory and on
// disk, with its folders ready, without starting it.
func accountEngine(t *testing.T, s settings.Settings) *Engine {
	t.Helper()
	paths := testPaths(t.TempDir())
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	e := New(paths, "test")
	s.Cache.Dir = paths.CacheDir
	if err := settings.Save(paths.SettingsFile, s, nil); err != nil {
		t.Fatal(err)
	}
	e.settings = s
	return e
}

// Where upper and lower case are the same, base64 names would let two
// encrypted names land on one file.
func TestBase64RefusedWhereCaseIsIgnored(t *testing.T) {
	s := settings.Defaults(`C:\cache`)
	s.Accounts = []settings.Account{
		{ID: "aonedrive", Service: cloud.OneDrive, Name: "OneDrive"},
		{ID: "adropbox", Service: cloud.Dropbox, Name: "Dropbox"},
		{ID: "adrive", Service: cloud.Drive, Name: "Google Drive"},
	}
	e := accountEngine(t, s)
	keys := vault.NewDefault("Vault")
	keys.Password = "a password for the test"
	keys.FilenameEncoding = vault.EncodingBase64
	for _, c := range []struct {
		account string
		allowed bool
	}{{"aonedrive", false}, {"adropbox", false}, {"adrive", true}} {
		_, err := e.placeNewVaultLocked(NewVault{Kind: settings.KindCloud, Account: c.account, Config: keys})
		if (err == nil) != c.allowed {
			t.Errorf("base64 in %s: got %v, want allowed %v", c.account, err, c.allowed)
		}
	}
	if _, err := e.placeNewVaultLocked(NewVault{Kind: settings.KindCloud, Account: "amissing", Config: keys}); err == nil {
		t.Error("a vault in an unknown account was accepted")
	}
}

func TestAccountChanges(t *testing.T) {
	s := settings.Defaults(`C:\cache`)
	s.Accounts = []settings.Account{
		{ID: "adrive", Service: cloud.Drive, Name: "Google Drive"},
		{ID: "aonedrive", Service: cloud.OneDrive, Name: "OneDrive"},
	}
	s.Vaults = []settings.Vault{{ID: "v1", Name: "Films", Kind: settings.KindCloud, Account: "adrive", Letter: "Z"}}
	e := accountEngine(t, s)
	ctx := context.Background()

	if err := e.RemoveAccount(ctx, "adrive"); err == nil || !strings.Contains(err.Error(), "Films") {
		t.Errorf("removing an account with a vault: got %v, want a refusal naming the vault", err)
	}
	if err := e.SetAccountApp("adrive", "", ""); err == nil {
		t.Error("Google Drive accepted rclone's app")
	}
	if err := e.SetAccountApp("aonedrive", "b15665d9-eda6-4092-8539-0eec376afd59", "secret1234"); err == nil {
		t.Error("OneDrive accepted an own app")
	}
	if err := e.SetAccountApp("adrive", "123-own.apps.googleusercontent.com", "GOCSPX-own"); err != nil {
		t.Fatal(err)
	}
	if err := e.RenameAccount("aonedrive", "google drive"); err == nil {
		t.Error("a name taken by another account was accepted")
	}
	if err := e.RenameAccount("aonedrive", "Work"); err != nil {
		t.Fatal(err)
	}
	if err := e.RemoveAccount(ctx, "aonedrive"); err != nil {
		t.Fatal(err)
	}

	stored, err := settings.Load(e.paths.SettingsFile, `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Accounts) != 1 || stored.Accounts[0].App.ClientID != "123-own.apps.googleusercontent.com" {
		t.Fatalf("stored accounts %+v; want Google Drive with its own app, OneDrive gone", stored.Accounts)
	}
	snap := e.Snapshot()
	if len(snap.Accounts) != 1 || !snap.Accounts[0].OwnApp || snap.Accounts[0].SignedInWithOwnApp || snap.Accounts[0].VaultCount != 1 {
		t.Fatalf("snapshot %+v", snap.Accounts)
	}
}

// rclone renews tokens in its memory only. The renewed token must reach the
// settings file, or a Microsoft sign-in would end 90 days after it was made.
// The renewal is simulated by giving rclone another token; no cloud service
// is asked.
func TestKeepRenewedSignIn(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("rclone not found in PATH")
	}
	const oldToken = `{"access_token":"old","token_type":"Bearer","refresh_token":"old","expiry":"2030-01-01T00:00:00Z"}`
	const newToken = `{"access_token":"new","token_type":"Bearer","refresh_token":"renewed","expiry":"2030-01-02T00:00:00Z"}`
	s := settings.Defaults(`C:\cache`)
	s.Accounts = []settings.Account{{ID: "adropbox", Service: cloud.Dropbox, Name: "Dropbox",
		Remote: map[string]string{"type": cloud.Dropbox, "token": oldToken}}}
	e := accountEngine(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if err := e.prepareLocked(ctx); err != nil { // registers the account with rclone
		t.Fatal(err)
	}
	client := clientOf(e)
	t.Cleanup(func() { _ = rcd.Stop(context.Background(), client, e.paths.StateFile) })

	if err := e.keepRenewedSignInLocked(ctx, client, "adropbox"); err != nil {
		t.Fatal(err)
	}
	if a, _ := e.currentSettings().Account("adropbox"); a.Remote["token"] != oldToken {
		t.Fatalf("token changed although rclone kept it: %q", a.Remote["token"])
	}

	renewed := map[string]string{"type": cloud.Dropbox, "token": newToken}
	if err := cloud.Restore(ctx, client, remoteName("adropbox"), renewed); err != nil {
		t.Fatal(err)
	}
	if err := e.keepRenewedSignInLocked(ctx, client, "adropbox"); err != nil {
		t.Fatal(err)
	}
	stored, err := settings.Load(e.paths.SettingsFile, `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := stored.Account("adropbox"); a.Remote["token"] != newToken {
		t.Fatalf("stored token %q; want the renewed one", a.Remote["token"])
	}
}
