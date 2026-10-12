// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package settings

import (
	"bytes"
	"maps"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/burnersen/CryptBridge/windows/internal/passlock"
)

// TestMigrateCopy converts a COPY of a real settings file of an older
// CryptBridge and reports what came through — never a secret. It only runs
// when CRYPTBRIDGE_MIGRATE_COPY names the copy; the copy is changed, so
// never point it at the real file. A locked copy is converted completely
// only when CRYPTBRIDGE_MIGRATE_PASSWORD holds its app password.
func TestMigrateCopy(t *testing.T) {
	path := os.Getenv("CRYPTBRIDGE_MIGRATE_COPY")
	if path == "" {
		t.Skip("CRYPTBRIDGE_MIGRATE_COPY not set")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	format, older := olderFormatOf(path)
	if !older {
		t.Fatal("the copy is not in an older format")
	}
	t.Logf("format: %q", bytes.TrimSpace(olderFormats[format].header))

	loaded, err := Load(path, `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	report(t, "converted", loaded)
	password := os.Getenv("CRYPTBRIDGE_MIGRATE_PASSWORD")
	var key *passlock.Key
	if loaded.AppLock {
		if password == "" {
			t.Log("the copy is locked with an app password; set CRYPTBRIDGE_MIGRATE_PASSWORD to convert its secrets too")
			return
		}
		if loaded, key, err = Unlock(path, `C:\cache`, password); err != nil {
			t.Fatal(err)
		}
		report(t, "unlocked", loaded)
	}

	if err := Save(path, loaded, key); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + olderFormats[format].backupSuffix)
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("backup: %v (identical to the original copy: %v)", err, bytes.Equal(backup, original))
	}
	again := loaded
	if loaded.AppLock {
		again, _, err = Unlock(path, `C:\cache`, password)
	} else {
		again, err = Load(path, `C:\cache`)
	}
	if err != nil {
		t.Fatal(err)
	}
	report(t, "saved and read again", again)
	if !reflect.DeepEqual(again.Accounts, loaded.Accounts) || !reflect.DeepEqual(again.Vaults, loaded.Vaults) {
		t.Fatal("accounts or vaults changed between converting and reading the new file")
	}
}

// report logs what the settings hold, without any secret.
func report(t *testing.T, label string, s Settings) {
	t.Helper()
	t.Logf("%s: app lock %v, auto reconnect %v, theme %q, %d account(s), %d vault(s)",
		label, s.AppLock, s.AutoReconnect, s.Theme, len(s.Accounts), len(s.Vaults))
	for _, a := range s.Accounts {
		t.Logf("  account id %q service %q name %q signed in %v, own app %v, rclone settings %v",
			a.ID, a.Service, a.Name, a.SignedIn(), a.App.IsSet(), slices.Sorted(maps.Keys(a.Remote)))
	}
	for _, v := range s.Vaults {
		t.Logf("  vault id %q name %q kind %q account %q letter %s auto-connect %v, keys %v, salt %v, folder name %d characters",
			v.ID, v.Name, v.Kind, v.Account, v.Letter, v.AutoConnect, v.HasKeys(), v.Config.Password2 != "", len(v.Config.NormalisedPath()))
	}
}
