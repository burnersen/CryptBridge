// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/burnersen/CryptBridge/windows/internal/cloud"
	"github.com/burnersen/CryptBridge/windows/internal/passlock"
	"github.com/burnersen/CryptBridge/windows/internal/testenv"
)

// TestSeedDevProfile writes a settings file for trying the real program in
// a separate test profile, with the developer's test vault. It only runs
// when CRYPTBRIDGE_SEED_PROFILE names the folder that will be used as
// APPDATA and LOCALAPPDATA of the test profile, e.g.:
//
//	CRYPTBRIDGE_SEED_PROFILE=C:\temp\cbprofile CRYPTBRIDGE_SEED_LETTER=Y go test ./internal/settings -run Seed
func TestSeedDevProfile(t *testing.T) {
	profile := os.Getenv("CRYPTBRIDGE_SEED_PROFILE")
	if profile == "" {
		t.Skip("CRYPTBRIDGE_SEED_PROFILE not set")
	}
	_, account, vaultCfg := testenv.Live(t)
	s := Defaults(filepath.Join(profile, "CryptBridge", "cache"))
	s.Accounts = []Account{{
		ID: MigratedAccountID, Service: cloud.PCloud, Name: migratedAccountName,
		Remote: map[string]string{"type": cloud.PCloud, "hostname": account.Hostname, "token": account.Token},
	}}
	test := Vault{ID: MigratedVaultID, Name: "Test vault", Kind: KindCloud, Account: MigratedAccountID,
		Letter: DefaultDriveLetter, AutoConnect: true, Config: vaultCfg}
	if letter := os.Getenv("CRYPTBRIDGE_SEED_LETTER"); letter != "" {
		test.Letter = letter
	}
	s.Vaults = []Vault{test}
	// CRYPTBRIDGE_SEED_APP_PASSWORD turns the app lock on with that password.
	var key *passlock.Key
	if password := os.Getenv("CRYPTBRIDGE_SEED_APP_PASSWORD"); password != "" {
		var err error
		if key, err = passlock.NewKey(password); err != nil {
			t.Fatal(err)
		}
		s.AppLock = true
		s.LockOffered = true
	}
	path := filepath.Join(profile, "CryptBridge", "settings.dat")
	if err := Save(path, s, key); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}
