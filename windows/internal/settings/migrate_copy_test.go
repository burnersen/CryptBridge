// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package settings

import (
	"bytes"
	"os"
	"testing"
)

// TestMigrateCopy converts a COPY of a real settings file of CryptBridge
// 0.3 and reports what came through — never a secret. It only runs when
// CRYPTBRIDGE_MIGRATE_COPY names the copy; the copy is changed, so never
// point it at the real file.
func TestMigrateCopy(t *testing.T) {
	path := os.Getenv("CRYPTBRIDGE_MIGRATE_COPY")
	if path == "" {
		t.Skip("CRYPTBRIDGE_MIGRATE_COPY not set")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("version 1 file: %v", IsLegacy(path))

	loaded, err := Load(path, `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	report := func(label string, s Settings) {
		t.Logf("%s: signed in %v, own app %v, app lock %v, auto reconnect %v, theme %q, %d vault(s)",
			label, s.SignedIn(), s.PCloudApp.IsSet(), s.AppLock, s.AutoReconnect, s.Theme, len(s.Vaults))
		for _, v := range s.Vaults {
			t.Logf("  vault id %q name %q kind %q letter %s auto-connect %v, keys %v, salt %v, folder name %d characters",
				v.ID, v.Name, v.Kind, v.Letter, v.AutoConnect, v.HasKeys(), v.Config.Password2 != "", len(v.Config.NormalisedPath()))
		}
	}
	report("converted", loaded)
	if loaded.AppLock {
		t.Log("the copy is locked with an app password: its keys are converted when unlocking (see TestConvertLockedVersion1File)")
		return
	}

	if err := Save(path, loaded, nil); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + LegacyBackupSuffix)
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("backup: %v (identical to the original copy: %v)", err, bytes.Equal(backup, original))
	}
	again, err := Load(path, `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	report("saved and read again", again)
	if len(again.Vaults) != len(loaded.Vaults) || (len(again.Vaults) > 0 && again.Vaults[0].Config != loaded.Vaults[0].Config) {
		t.Fatal("the vault changed between converting and reading the new file")
	}
}
