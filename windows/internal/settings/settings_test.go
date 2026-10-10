// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "settings.dat"), `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Drive.Letter != "Z" || s.Drive.CacheMaxSize != "20G" || s.Drive.CacheDir != `C:\cache` || s.SignedIn() || s.HasVault() {
		t.Fatalf("unexpected defaults: %+v", s)
	}
}

func TestSaveLoadRoundTripIsEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.dat")
	want := Defaults(`C:\cache`)
	want.PCloud = pcloud.Account{Hostname: pcloud.HostEU, Token: `{"access_token":"VERY-SECRET-TOKEN"}`}
	want.Vault = vault.NewDefault("Vault")
	want.Vault.Password = "VERY-SECRET-PASSWORD"
	want.Vault.Password2 = "VERY-SECRET-SALT"
	want.Drive.Letter = "X"

	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"VERY-SECRET-TOKEN", "VERY-SECRET-PASSWORD", "VERY-SECRET-SALT", "Vault"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("settings file contains %q in readable form", secret)
		}
	}

	got, err := Load(path, `C:\other`)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip changed settings:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadRejectsForeignFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.dat")
	if err := os.WriteFile(path, []byte("not a settings file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Fatal("a file without the header must be rejected")
	}
	if err := os.WriteFile(path, append(append([]byte{}, fileHeader...), 1, 2, 3), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Fatal("a damaged encrypted part must be rejected")
	}
}
