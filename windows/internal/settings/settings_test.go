// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/burnersen/CryptBridge/windows/internal/passlock"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "settings.dat"), `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Cache.MaxSize != "20G" || s.Cache.Dir != `C:\cache` || s.Theme != ThemeAuto || !s.AutoReconnect ||
		s.SignedIn() || len(s.Vaults) != 0 {
		t.Fatalf("unexpected defaults: %+v", s)
	}
	if s.LAN != (LAN{Port: DefaultLANPort, User: DefaultLANUser}) {
		t.Fatalf("LAN defaults: %+v; want off, port %d, user %q", s.LAN, DefaultLANPort, DefaultLANUser)
	}
}

// twoVaults returns settings with a pCloud and a local vault whose secrets
// are easy to search for in the file.
func twoVaults() Settings {
	s := Defaults(`C:\cache`)
	s.PCloud = pcloud.Account{Hostname: pcloud.HostEU, Token: `{"access_token":"VERY-SECRET-TOKEN"}`}
	s.PCloudApp = pcloud.App{ClientID: "OwnClientId", ClientSecret: "VERY-SECRET-CLIENT-SECRET"}
	s.SignInAppID = "OwnClientId"
	cloud := vault.NewDefault("SecretCloudFolder")
	cloud.Password = "VERY-SECRET-PASSWORD"
	cloud.Password2 = "VERY-SECRET-SALT"
	local := vault.NewDefault(`E:\SecretLocalFolder`)
	local.Password = "VERY-SECRET-LOCAL-PASSWORD"
	s.Vaults = []Vault{
		{ID: "vault", Name: "Films", Kind: KindPCloud, Letter: "Z", AutoConnect: true, Config: cloud},
		{ID: "v1a2b3c4d", Name: "Private", Kind: KindLocal, Letter: "Y",
			LastSize: &SizeRecord{Bytes: 42, Files: 1, At: "2026-10-11T12:00:00Z"}, Config: local},
	}
	s.LAN = LAN{Enabled: true, Port: 8090, User: "tv", Password: "VERY-SECRET-LAN-PASSWORD", AllowChanges: true}
	return s
}

var secretsInTwoVaults = []string{"VERY-SECRET-TOKEN", "VERY-SECRET-CLIENT-SECRET", "VERY-SECRET-PASSWORD",
	"VERY-SECRET-SALT", "VERY-SECRET-LOCAL-PASSWORD", "SecretCloudFolder", "SecretLocalFolder", "VERY-SECRET-LAN-PASSWORD"}

func TestSaveLoadRoundTripIsEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.dat")
	want := twoVaults()
	want.Theme = ThemeLight

	if err := Save(path, want, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, fileHeader) {
		t.Fatalf("file starts with %q, want the version 2 header", raw[:20])
	}
	for _, secret := range append(secretsInTwoVaults, "Films", "Private") {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("settings file contains %q in readable form", secret)
		}
	}

	got, err := Load(path, `C:\other`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed settings:\n got %+v\nwant %+v", got, want)
	}
}

// With the app lock on, the secrets must not be readable even inside the
// DPAPI layer, which anyone signed in to the Windows account can open.
func TestAppLockSealsSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.dat")
	want := twoVaults()
	want.AppLock = true
	want.LockOffered = true

	if err := Save(path, want, nil); !errors.Is(err, ErrNeedsKey) {
		t.Fatalf("save without key: got %v, want ErrNeedsKey", err)
	}
	key, err := passlock.NewKey("app password for the test")
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, want, key); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := unprotect(raw[len(fileHeader):]) // what DPAPI shows anyone in the account
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range append(secretsInTwoVaults, "OwnClientId") {
		if bytes.Contains(inner, []byte(secret)) {
			t.Fatalf("%q readable behind DPAPI although the app lock is on", secret)
		}
	}

	locked, err := Load(path, `C:\other`)
	if err != nil {
		t.Fatal(err)
	}
	if !locked.AppLock || locked.SignedIn() || len(locked.Vaults) != 2 {
		t.Fatalf("locked load: %+v; want the vault list without secrets", locked)
	}
	for _, v := range locked.Vaults {
		if v.HasKeys() || v.Config != (vault.Config{}) {
			t.Fatalf("locked load knows the keys of vault %q", v.Name)
		}
	}
	if locked.Vaults[1].Name != "Private" || locked.Vaults[1].Letter != "Y" {
		t.Fatalf("locked load lost name or letter: %+v", locked.Vaults[1])
	}
	if wantLAN := (LAN{Enabled: true, Port: 8090, User: "tv", AllowChanges: true}); locked.LAN != wantLAN {
		t.Fatalf("locked load: LAN %+v; want %+v (everything but the password)", locked.LAN, wantLAN)
	}

	if _, _, err := Unlock(path, `C:\other`, "wrong password!"); !errors.Is(err, passlock.ErrWrongPassword) {
		t.Fatalf("wrong password: got %v", err)
	}
	got, gotKey, err := Unlock(path, `C:\other`, "app password for the test")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unlock changed settings:\n got %+v\nwant %+v", got, want)
	}

	// Changes saved with the key from Unlock open with the same password.
	got.Vaults[1].Letter = "X"
	if err := Save(path, got, gotKey); err != nil {
		t.Fatal(err)
	}
	again, _, err := Unlock(path, `C:\other`, "app password for the test")
	if err != nil || again.Vaults[1].Letter != "X" || again.Vaults[1].Config.Password != "VERY-SECRET-LOCAL-PASSWORD" {
		t.Fatalf("after resave: %+v, %v", again, err)
	}
}

// writeVersion1 writes a settings file as CryptBridge 0.3 did.
func writeVersion1(t *testing.T, path string, content any) []byte {
	t.Helper()
	plain, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	data := append(append([]byte{}, legacyHeader...), protected...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

// The vault of CryptBridge 0.3 must come through the update unchanged: same
// keys, same letter, same rclone name (its cache), and the old file is kept.
func TestConvertVersion1File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.dat")
	old := vault.NewDefault("CryptBridge")
	old.Password = "OLD-PASSWORD"
	old.Password2 = "OLD-SALT"
	original := writeVersion1(t, path, map[string]any{
		"pcloud":        pcloud.Account{Hostname: pcloud.HostEU, Token: "OLD-TOKEN"},
		"vault":         old,
		"drive":         map[string]string{"letter": "X", "cacheDir": `D:\Cache`, "cacheMaxSize": "30G", "cacheMinFreeSpace": "5G"},
		"autoConnect":   false,
		"autoReconnect": true,
	})
	if !IsLegacy(path) {
		t.Fatal("IsLegacy: false for a version 1 file")
	}

	got, err := Load(path, `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	want := Vault{ID: MigratedVaultID, Name: "Vault", Kind: KindPCloud, Letter: "X", AutoConnect: false, Config: old}
	if len(got.Vaults) != 1 || !reflect.DeepEqual(got.Vaults[0], want) {
		t.Fatalf("converted vaults %+v; want [%+v]", got.Vaults, want)
	}
	if got.PCloud.Token != "OLD-TOKEN" || got.PCloudApp.IsSet() || got.Cache != (Cache{Dir: `D:\Cache`, MaxSize: "30G", MinFreeSpace: "5G"}) ||
		!got.AutoReconnect || got.Theme != ThemeAuto || got.LAN != Defaults(`C:\cache`).LAN {
		t.Fatalf("converted settings: %+v", got)
	}

	// The first save keeps the old file as a backup, byte for byte.
	if err := Save(path, got, nil); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + LegacyBackupSuffix)
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("backup of the version 1 file: %v (same bytes: %v)", err, bytes.Equal(backup, original))
	}
	if IsLegacy(path) {
		t.Fatal("still a version 1 file after saving")
	}
	// A later save never touches the backup.
	got.Vaults[0].Name = "Renamed"
	if err := Save(path, got, nil); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path + LegacyBackupSuffix); !bytes.Equal(again, original) {
		t.Fatal("the backup changed with a later save")
	}
	if err := RemoveLegacyBackup(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + LegacyBackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backup still there after RemoveLegacyBackup")
	}
	if err := RemoveLegacyBackup(path); err != nil {
		t.Fatalf("removing a missing backup: %v", err)
	}
}

// A locked version 1 file has the vault keys only in its sealed part: the
// vault is listed without them and gets them when unlocking.
func TestConvertLockedVersion1File(t *testing.T) {
	const appPassword = "app password for the test"
	old := vault.NewDefault("CryptBridge")
	old.Password = "OLD-PASSWORD"

	for _, tc := range []struct {
		name       string
		sealed     map[string]any
		wantVaults int
	}{
		{"with vault", map[string]any{"pcloud": pcloud.Account{Hostname: pcloud.HostEU, Token: "T"}, "vault": old}, 1},
		{"without vault", map[string]any{"pcloud": pcloud.Account{Hostname: pcloud.HostEU, Token: "T"}, "vault": vault.Config{}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.dat")
			key, err := passlock.NewKey(appPassword)
			if err != nil {
				t.Fatal(err)
			}
			secretJSON, err := json.Marshal(tc.sealed)
			if err != nil {
				t.Fatal(err)
			}
			sealed, err := key.Seal(secretJSON)
			if err != nil {
				t.Fatal(err)
			}
			writeVersion1(t, path, map[string]any{
				"drive":       map[string]string{"letter": "Y", "cacheDir": `C:\cache`, "cacheMaxSize": "20G", "cacheMinFreeSpace": "10G"},
				"autoConnect": true,
				"appLock":     true,
				"lockOffered": true,
				"sealed":      sealed,
			})

			locked, err := Load(path, `C:\cache`)
			if err != nil {
				t.Fatal(err)
			}
			if len(locked.Vaults) != 1 || locked.Vaults[0].ID != MigratedVaultID || locked.Vaults[0].Letter != "Y" || locked.Vaults[0].HasKeys() {
				t.Fatalf("locked version 1 load: %+v; want one placeholder vault on Y:", locked.Vaults)
			}
			unlocked, _, err := Unlock(path, `C:\cache`, appPassword)
			if err != nil {
				t.Fatal(err)
			}
			if len(unlocked.Vaults) != tc.wantVaults {
				t.Fatalf("after unlocking: %d vaults %+v; want %d", len(unlocked.Vaults), unlocked.Vaults, tc.wantVaults)
			}
			if tc.wantVaults == 1 && (unlocked.Vaults[0].Config != old || !unlocked.AutoReconnect || unlocked.PCloud.Token != "T") {
				t.Fatalf("after unlocking: %+v", unlocked)
			}
		})
	}
}

// Changing a clone must never change the settings it came from: the engine
// prepares changes on a clone and keeps the old settings if saving fails.
func TestCloneIsIndependent(t *testing.T) {
	s := twoVaults()
	c := s.Clone()
	c.Vaults[0].Name = "Changed"
	c.Vaults[1].LastSize.Bytes = 1
	c.Vaults = append(c.Vaults, Vault{ID: "extra"})
	if s.Vaults[0].Name != "Films" || s.Vaults[1].LastSize.Bytes != 42 || len(s.Vaults) != 2 {
		t.Fatalf("changing the clone changed the original: %+v", s.Vaults)
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
	if err := os.WriteFile(path, []byte("CryptBridge-Settings-3\nfrom the future"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Fatal("a file of a newer format must be rejected")
	}
	if err := os.WriteFile(path, append(append([]byte{}, fileHeader...), 1, 2, 3), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Fatal("a damaged encrypted part must be rejected")
	}
}

// A file of CryptBridge 0.4 has no LAN settings yet: LAN access is off, with
// the default port and user, and everything else is read as before.
func TestLoadFileWithoutLANSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.dat")
	plain, err := json.Marshal(map[string]any{
		"vaults":        []Vault{{ID: "vault", Name: "Films", Kind: KindPCloud, Letter: "Z"}},
		"cache":         Cache{Dir: `D:\Cache`, MaxSize: "30G", MinFreeSpace: "5G"},
		"autoReconnect": false,
		"theme":         ThemeDark,
	})
	if err != nil {
		t.Fatal(err)
	}
	protected, err := protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte{}, fileHeader...), protected...), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path, `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	if got.LAN != (LAN{Port: DefaultLANPort, User: DefaultLANUser}) {
		t.Fatalf("LAN %+v; want the defaults", got.LAN)
	}
	if len(got.Vaults) != 1 || got.Cache.MaxSize != "30G" || got.AutoReconnect || got.Theme != ThemeDark {
		t.Fatalf("the other settings changed: %+v", got)
	}
}
