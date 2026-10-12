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

	"github.com/burnersen/CryptBridge/windows/internal/cloud"
	"github.com/burnersen/CryptBridge/windows/internal/passlock"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// Headers of the older formats, as CryptBridge 0.3 and 0.6 wrote them.
var (
	headerVersion1 = []byte("CryptBridge-Settings-1\n")
	headerVersion2 = []byte("CryptBridge-Settings-2\n")
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "settings.dat"), `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Cache.MaxSize != "20G" || s.Cache.Dir != `C:\cache` || s.Theme != ThemeAuto || !s.AutoReconnect ||
		len(s.Accounts) != 0 || len(s.Vaults) != 0 {
		t.Fatalf("unexpected defaults: %+v", s)
	}
	want := LAN{Streaming: LANAccess{Port: 8080, User: "stream"}, Full: LANAccess{Port: 8081, User: "full"}}
	if s.LAN != want {
		t.Fatalf("LAN defaults: %+v; want %+v (both off)", s.LAN, want)
	}
}

// twoVaults returns settings with two accounts, a cloud and a local vault,
// whose secrets are easy to search for in the file.
func twoVaults() Settings {
	s := Defaults(`C:\cache`)
	s.Accounts = []Account{
		{
			ID: MigratedAccountID, Service: cloud.PCloud, Name: "pCloud",
			App: cloud.App{ClientID: "OwnClientId", ClientSecret: "VERY-SECRET-CLIENT-SECRET"},
			Remote: map[string]string{"type": cloud.PCloud, "hostname": pcloud.HostEU, "token": `{"access_token":"VERY-SECRET-TOKEN"}`,
				"client_id": "OwnClientId", "client_secret": "VERY-SECRET-CLIENT-SECRET"},
		},
		{
			ID: "a1b2c3d4e", Service: cloud.Drive, Name: "Google Drive",
			App:    cloud.App{ClientID: "123-own.apps.googleusercontent.com", ClientSecret: "VERY-SECRET-GOOGLE-SECRET"},
			Remote: map[string]string{"type": cloud.Drive, "token": `{"access_token":"VERY-SECRET-DRIVE-TOKEN"}`},
		},
	}
	cloudVault := vault.NewDefault("SecretCloudFolder")
	cloudVault.Password = "VERY-SECRET-PASSWORD"
	cloudVault.Password2 = "VERY-SECRET-SALT"
	local := vault.NewDefault(`E:\SecretLocalFolder`)
	local.Password = "VERY-SECRET-LOCAL-PASSWORD"
	s.Vaults = []Vault{
		{ID: "vault", Name: "Films", Kind: KindCloud, Account: MigratedAccountID, Letter: "Z", AutoConnect: true, Config: cloudVault},
		{ID: "v1a2b3c4d", Name: "Private", Kind: KindLocal, Letter: "Y",
			LastSize: &SizeRecord{Bytes: 42, Files: 1, At: "2026-10-11T12:00:00Z"}, Config: local},
	}
	s.LAN = LAN{
		Streaming: LANAccess{Enabled: true, Port: 8090, User: "tv", Password: "VERY-SECRET-LAN-PASSWORD"},
		Full:      LANAccess{Enabled: true, Port: 8091, User: "phone", Password: "VERY-SECRET-FULL-LAN-PASSWORD"},
	}
	return s
}

var secretsInTwoVaults = []string{"VERY-SECRET-TOKEN", "VERY-SECRET-CLIENT-SECRET", "VERY-SECRET-DRIVE-TOKEN",
	"VERY-SECRET-GOOGLE-SECRET", "VERY-SECRET-PASSWORD", "VERY-SECRET-SALT", "VERY-SECRET-LOCAL-PASSWORD",
	"SecretCloudFolder", "SecretLocalFolder", "VERY-SECRET-LAN-PASSWORD", "VERY-SECRET-FULL-LAN-PASSWORD"}

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
	if !bytes.HasPrefix(raw, []byte("CryptBridge-Settings-3\n")) {
		t.Fatalf("file starts with %q, want the version 3 header", raw[:20])
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
	if IsLegacy(path) {
		t.Fatal("IsLegacy: true for a file in the current format")
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
	for _, secret := range append(secretsInTwoVaults, "OwnClientId", "123-own.apps.googleusercontent.com") {
		if bytes.Contains(inner, []byte(secret)) {
			t.Fatalf("%q readable behind DPAPI although the app lock is on", secret)
		}
	}

	locked, err := Load(path, `C:\other`)
	if err != nil {
		t.Fatal(err)
	}
	if !locked.AppLock || len(locked.Accounts) != 2 || len(locked.Vaults) != 2 {
		t.Fatalf("locked load: %+v; want the account and vault lists without secrets", locked)
	}
	for _, a := range locked.Accounts {
		if a.SignedIn() || a.App.IsSet() || a.Remote != nil {
			t.Fatalf("locked load knows the secrets of account %q", a.Name)
		}
	}
	if locked.Accounts[1].Name != "Google Drive" || locked.Accounts[1].Service != cloud.Drive {
		t.Fatalf("locked load lost name or service: %+v", locked.Accounts[1])
	}
	for _, v := range locked.Vaults {
		if v.HasKeys() || v.Config != (vault.Config{}) {
			t.Fatalf("locked load knows the keys of vault %q", v.Name)
		}
	}
	if locked.Vaults[1].Name != "Private" || locked.Vaults[1].Letter != "Y" || locked.Vaults[0].Account != MigratedAccountID {
		t.Fatalf("locked load lost name, letter or account: %+v", locked.Vaults)
	}
	wantLAN := want.LAN
	wantLAN.Streaming.Password, wantLAN.Full.Password = "", ""
	if locked.LAN != wantLAN {
		t.Fatalf("locked load: LAN %+v; want %+v (everything but the passwords)", locked.LAN, wantLAN)
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
	got.Accounts[1].Remote["token"] = "RENEWED-TOKEN"
	if err := Save(path, got, gotKey); err != nil {
		t.Fatal(err)
	}
	again, _, err := Unlock(path, `C:\other`, "app password for the test")
	if err != nil || again.Vaults[1].Letter != "X" || again.Vaults[1].Config.Password != "VERY-SECRET-LOCAL-PASSWORD" ||
		again.Accounts[1].Remote["token"] != "RENEWED-TOKEN" {
		t.Fatalf("after resave: %+v, %v", again, err)
	}
}

// writeOlderFormat writes a settings file as an older CryptBridge did.
func writeOlderFormat(t *testing.T, path string, header []byte, content any) []byte {
	t.Helper()
	plain, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	data := append(append([]byte{}, header...), protected...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

// sealForTest seals content as the locked part of an older file.
func sealForTest(t *testing.T, password string, content any) passlock.Sealed {
	t.Helper()
	key, err := passlock.NewKey(password)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := key.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// sameAccounts compares account lists; no list and an empty one are the same.
func sameAccounts(got, want []Account) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

// migratedPCloud is the account a version 1 or 2 file signed in to pCloud
// with rclone's app becomes.
func migratedPCloud(token string) Account {
	return Account{
		ID: MigratedAccountID, Service: cloud.PCloud, Name: "pCloud",
		Remote: map[string]string{"type": cloud.PCloud, "hostname": pcloud.HostEU, "token": token},
	}
}

// The vault of CryptBridge 0.3 must come through the update unchanged: same
// keys, same letter, same rclone name (its cache), and the old file is kept.
func TestConvertVersion1File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.dat")
	old := vault.NewDefault("CryptBridge")
	old.Password = "OLD-PASSWORD"
	old.Password2 = "OLD-SALT"
	original := writeOlderFormat(t, path, headerVersion1, map[string]any{
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
	want := Vault{ID: MigratedVaultID, Name: "Vault", Kind: KindCloud, Account: MigratedAccountID, Letter: "X", AutoConnect: false, Config: old}
	if len(got.Vaults) != 1 || !reflect.DeepEqual(got.Vaults[0], want) {
		t.Fatalf("converted vaults %+v; want [%+v]", got.Vaults, want)
	}
	if !reflect.DeepEqual(got.Accounts, []Account{migratedPCloud("OLD-TOKEN")}) {
		t.Fatalf("converted accounts %+v", got.Accounts)
	}
	if got.Cache != (Cache{Dir: `D:\Cache`, MaxSize: "30G", MinFreeSpace: "5G"}) ||
		!got.AutoReconnect || got.Theme != ThemeAuto || got.LAN != Defaults(`C:\cache`).LAN {
		t.Fatalf("converted settings: %+v", got)
	}

	// The first save keeps the old file as a backup, byte for byte.
	if err := Save(path, got, nil); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".v1-backup")
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("backup of the version 1 file: %v (same bytes: %v)", err, bytes.Equal(backup, original))
	}
	if IsLegacy(path) {
		t.Fatal("still an older format after saving")
	}
	// A later save never touches the backup.
	got.Vaults[0].Name = "Renamed"
	if err := Save(path, got, nil); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path + ".v1-backup"); !bytes.Equal(again, original) {
		t.Fatal("the backup changed with a later save")
	}
	if err := RemoveLegacyBackups(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".v1-backup"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backup still there after RemoveLegacyBackups")
	}
	if err := RemoveLegacyBackups(path); err != nil {
		t.Fatalf("removing a missing backup: %v", err)
	}
}

// A locked version 1 file has the vault keys and the sign-in only in its
// sealed part: vault and account are listed without them and get them when
// unlocking — or disappear, if there was nothing behind them.
func TestConvertLockedVersion1File(t *testing.T) {
	const appPassword = "app password for the test"
	old := vault.NewDefault("CryptBridge")
	old.Password = "OLD-PASSWORD"
	signedIn := pcloud.Account{Hostname: pcloud.HostEU, Token: "T"}

	for _, tc := range []struct {
		name         string
		sealed       map[string]any
		wantVaults   int
		wantAccounts int
	}{
		{"with vault", map[string]any{"pcloud": signedIn, "vault": old}, 1, 1},
		{"signed in without vault", map[string]any{"pcloud": signedIn, "vault": vault.Config{}}, 0, 1},
		{"neither", map[string]any{"pcloud": pcloud.Account{}, "vault": vault.Config{}}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.dat")
			writeOlderFormat(t, path, headerVersion1, map[string]any{
				"drive":       map[string]string{"letter": "Y", "cacheDir": `C:\cache`, "cacheMaxSize": "20G", "cacheMinFreeSpace": "10G"},
				"autoConnect": true,
				"appLock":     true,
				"lockOffered": true,
				"sealed":      sealForTest(t, appPassword, tc.sealed),
			})

			locked, err := Load(path, `C:\cache`)
			if err != nil {
				t.Fatal(err)
			}
			if len(locked.Vaults) != 1 || locked.Vaults[0].ID != MigratedVaultID || locked.Vaults[0].Letter != "Y" || locked.Vaults[0].HasKeys() {
				t.Fatalf("locked version 1 load: %+v; want one placeholder vault on Y:", locked.Vaults)
			}
			if len(locked.Accounts) != 1 || locked.Accounts[0].SignedIn() {
				t.Fatalf("locked version 1 load: %+v; want one placeholder account", locked.Accounts)
			}
			unlocked, _, err := Unlock(path, `C:\cache`, appPassword)
			if err != nil {
				t.Fatal(err)
			}
			if len(unlocked.Vaults) != tc.wantVaults || len(unlocked.Accounts) != tc.wantAccounts {
				t.Fatalf("after unlocking: vaults %+v, accounts %+v; want %d and %d",
					unlocked.Vaults, unlocked.Accounts, tc.wantVaults, tc.wantAccounts)
			}
			if tc.wantVaults == 1 && (unlocked.Vaults[0].Config != old || !unlocked.AutoReconnect) {
				t.Fatalf("after unlocking: %+v", unlocked)
			}
			if tc.wantAccounts == 1 && !reflect.DeepEqual(unlocked.Accounts[0], migratedPCloud("T")) {
				t.Fatalf("after unlocking: account %+v", unlocked.Accounts[0])
			}
		})
	}
}

// The pCloud account of CryptBridge 0.4 to 0.6 becomes the first account of
// the list, with its own app; its vaults stay with it, and the old file is
// kept once.
func TestConvertVersion2File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.dat")
	films := vault.NewDefault("Films")
	films.Password = "FILMS-PASSWORD"
	local := vault.NewDefault(`E:\Private`)
	local.Password = "LOCAL-PASSWORD"
	original := writeOlderFormat(t, path, headerVersion2, map[string]any{
		"pcloud":      pcloud.Account{Hostname: pcloud.HostUS, Token: "V2-TOKEN"},
		"pcloudApp":   cloud.App{ClientID: "OwnClientId", ClientSecret: "OwnSecret"},
		"signInAppId": "OwnClientId",
		"vaults": []map[string]any{
			{"id": "vault", "name": "Films", "kind": "pcloud", "letter": "Z", "autoConnect": true, "config": films},
			{"id": "v1a2b3c4d", "name": "Private", "kind": "local", "letter": "Y", "config": local},
		},
		"theme": ThemeDark,
	})
	if !IsLegacy(path) {
		t.Fatal("IsLegacy: false for a version 2 file")
	}

	got, err := Load(path, `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	wantAccount := Account{
		ID: MigratedAccountID, Service: cloud.PCloud, Name: "pCloud",
		App: cloud.App{ClientID: "OwnClientId", ClientSecret: "OwnSecret"},
		Remote: map[string]string{"type": cloud.PCloud, "hostname": pcloud.HostUS, "token": "V2-TOKEN",
			"client_id": "OwnClientId", "client_secret": "OwnSecret"},
	}
	if !reflect.DeepEqual(got.Accounts, []Account{wantAccount}) {
		t.Fatalf("accounts %+v; want [%+v]", got.Accounts, wantAccount)
	}
	wantVaults := []Vault{
		{ID: "vault", Name: "Films", Kind: KindCloud, Account: MigratedAccountID, Letter: "Z", AutoConnect: true, Config: films},
		{ID: "v1a2b3c4d", Name: "Private", Kind: KindLocal, Letter: "Y", Config: local},
	}
	if !reflect.DeepEqual(got.Vaults, wantVaults) || got.Theme != ThemeDark {
		t.Fatalf("vaults %+v, theme %q", got.Vaults, got.Theme)
	}

	if err := Save(path, got, nil); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".v2-backup")
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("backup of the version 2 file: %v (same bytes: %v)", err, bytes.Equal(backup, original))
	}
	again, err := Load(path, `C:\cache`)
	if err != nil || !reflect.DeepEqual(again, got) || IsLegacy(path) {
		t.Fatalf("after saving: %+v, %v", again, err)
	}
	if err := RemoveLegacyBackups(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".v2-backup"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("backup still there after RemoveLegacyBackups")
	}
}

// An account appears only where there was something to keep: a sign-in, an
// own app, or a vault in pCloud.
func TestConvertVersion2FileWithoutSignIn(t *testing.T) {
	local := vault.NewDefault(`E:\Private`)
	local.Password = "LOCAL-PASSWORD"
	for _, tc := range []struct {
		name    string
		content map[string]any
		want    []Account
	}{
		{"local vaults only", map[string]any{
			"pcloud": pcloud.Account{}, "pcloudApp": cloud.App{},
			"vaults": []map[string]any{{"id": "v1", "name": "Private", "kind": "local", "letter": "Y", "config": local}},
		}, nil},
		{"own app, signed out", map[string]any{
			"pcloud": pcloud.Account{}, "pcloudApp": cloud.App{ClientID: "OwnClientId", ClientSecret: "OwnSecret"},
		}, []Account{{ID: MigratedAccountID, Service: cloud.PCloud, Name: "pCloud",
			App: cloud.App{ClientID: "OwnClientId", ClientSecret: "OwnSecret"}}}},
		{"signed in with an app since replaced", map[string]any{
			"pcloud":      pcloud.Account{Hostname: pcloud.HostEU, Token: "T"},
			"pcloudApp":   cloud.App{ClientID: "NewClientId", ClientSecret: "NewSecret"},
			"signInAppId": "OldClientId",
		}, []Account{{ID: MigratedAccountID, Service: cloud.PCloud, Name: "pCloud",
			App:    cloud.App{ClientID: "NewClientId", ClientSecret: "NewSecret"},
			Remote: map[string]string{"type": cloud.PCloud, "hostname": pcloud.HostEU, "token": "T", "client_id": "OldClientId"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.dat")
			writeOlderFormat(t, path, headerVersion2, tc.content)
			got, err := Load(path, `C:\cache`)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Accounts, tc.want) {
				t.Fatalf("accounts %+v; want %+v", got.Accounts, tc.want)
			}
		})
	}
}

// A locked file of CryptBridge 0.4 to 0.6 keeps the pCloud sign-in in its
// sealed part. The account is listed without it and gets it when
// unlocking; saved with the key, the new file opens the same way.
func TestConvertLockedVersion2File(t *testing.T) {
	const appPassword = "app password for the test"
	films := vault.NewDefault("Films")
	films.Password = "FILMS-PASSWORD"
	for _, tc := range []struct {
		name         string
		readable     []map[string]any // the vaults, as a locked 0.6 wrote them
		sealed       map[string]any
		wantAccounts []Account
	}{
		{"signed in with a pCloud vault",
			[]map[string]any{{"id": "vault", "name": "Films", "kind": "pcloud", "letter": "Z", "config": vault.Config{}}},
			map[string]any{"pcloud": pcloud.Account{Hostname: pcloud.HostEU, Token: "SEALED-TOKEN"},
				"pcloudApp": cloud.App{}, "vaultKeys": map[string]vault.Config{"vault": films}},
			[]Account{migratedPCloud("SEALED-TOKEN")}},
		{"never signed in, no pCloud vault", nil,
			map[string]any{"pcloud": pcloud.Account{}, "pcloudApp": cloud.App{}, "vaultKeys": map[string]vault.Config{}},
			nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.dat")
			writeOlderFormat(t, path, headerVersion2, map[string]any{
				"pcloud":    pcloud.Account{}, // what a locked 0.6 left readable
				"pcloudApp": cloud.App{},
				"vaults":    tc.readable,
				"appLock":   true,
				"sealed":    sealForTest(t, appPassword, tc.sealed),
			})

			locked, err := Load(path, `C:\cache`)
			if err != nil {
				t.Fatal(err)
			}
			if len(locked.Accounts) != 1 || locked.Accounts[0].ID != MigratedAccountID || locked.Accounts[0].SignedIn() {
				t.Fatalf("locked load: accounts %+v; want the pCloud placeholder", locked.Accounts)
			}
			got, key, err := Unlock(path, `C:\cache`, appPassword)
			if err != nil {
				t.Fatal(err)
			}
			if !sameAccounts(got.Accounts, tc.wantAccounts) {
				t.Fatalf("after unlocking: accounts %+v; want %+v", got.Accounts, tc.wantAccounts)
			}
			if len(tc.readable) == 1 && (len(got.Vaults) != 1 || got.Vaults[0].Config != films || got.Vaults[0].Account != MigratedAccountID) {
				t.Fatalf("after unlocking: vaults %+v", got.Vaults)
			}

			if err := Save(path, got, key); err != nil {
				t.Fatal(err)
			}
			again, _, err := Unlock(path, `C:\cache`, appPassword)
			if err != nil || !reflect.DeepEqual(again, got) {
				t.Fatalf("after saving: %+v, %v; want %+v", again, err, got)
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
	c.Accounts[1].Remote["token"] = "changed"
	c.Accounts[0].Name = "Changed"
	if s.Vaults[0].Name != "Films" || s.Vaults[1].LastSize.Bytes != 42 || len(s.Vaults) != 2 {
		t.Fatalf("changing the clone changed the original vaults: %+v", s.Vaults)
	}
	if s.Accounts[1].Remote["token"] == "changed" || s.Accounts[0].Name != "pCloud" {
		t.Fatalf("changing the clone changed the original accounts: %+v", s.Accounts)
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
	if err := os.WriteFile(path, []byte("CryptBridge-Settings-4\nfrom the future"), 0o600); err != nil {
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
	writeOlderFormat(t, path, headerVersion2, map[string]any{
		"vaults":        []map[string]any{{"id": "vault", "name": "Films", "kind": "pcloud", "letter": "Z"}},
		"cache":         Cache{Dir: `D:\Cache`, MaxSize: "30G", MinFreeSpace: "5G"},
		"autoReconnect": false,
		"theme":         ThemeDark,
	})

	got, err := Load(path, `C:\cache`)
	if err != nil {
		t.Fatal(err)
	}
	if got.LAN != defaultLAN() {
		t.Fatalf("LAN %+v; want the defaults", got.LAN)
	}
	if len(got.Vaults) != 1 || got.Vaults[0].Kind != KindCloud || got.Cache.MaxSize != "30G" || got.AutoReconnect || got.Theme != ThemeDark {
		t.Fatalf("the other settings changed: %+v", got)
	}
}

// The one LAN access of CryptBridge 0.5 becomes the access with the same
// rights, unchanged, so that the devices keep working; the other access
// stays off on a port of its own.
func TestConvertLANOfVersion05(t *testing.T) {
	tests := []struct {
		name string
		old  lanVersion1
		want LAN
	}{
		{"read only on 8080", lanVersion1{Enabled: true, Port: 8080, User: "tv", Password: "old-password-1"},
			LAN{Streaming: LANAccess{Enabled: true, Port: 8080, User: "tv", Password: "old-password-1"},
				Full: LANAccess{Port: 8081, User: "full"}}},
		{"read only on 8081", lanVersion1{Port: 8081, User: "cryptbridge"},
			LAN{Streaming: LANAccess{Port: 8081, User: "cryptbridge"}, Full: LANAccess{Port: 8080, User: "full"}}},
		{"changes allowed on 8080", lanVersion1{Enabled: true, Port: 8080, User: "cryptbridge", Password: "old-password-2", AllowChanges: true},
			LAN{Streaming: LANAccess{Port: 8081, User: "stream"},
				Full: LANAccess{Enabled: true, Port: 8080, User: "cryptbridge", Password: "old-password-2"}}},
		{"changes allowed on 9000", lanVersion1{Enabled: true, Port: 9000, User: "me", Password: "old-password-3", AllowChanges: true},
			LAN{Streaming: LANAccess{Port: 8080, User: "stream"},
				Full: LANAccess{Enabled: true, Port: 9000, User: "me", Password: "old-password-3"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.dat")
			writeOlderFormat(t, path, headerVersion2, map[string]any{"theme": ThemeDark, "lan": tt.old})

			got, err := Load(path, `C:\cache`)
			if err != nil {
				t.Fatal(err)
			}
			if got.LAN != tt.want || got.Theme != ThemeDark {
				t.Fatalf("LAN %+v; want %+v", got.LAN, tt.want)
			}
			// Saved again, the file has the new form and reads the same.
			if err := Save(path, got, nil); err != nil {
				t.Fatal(err)
			}
			again, err := Load(path, `C:\cache`)
			if err != nil || again.LAN != tt.want {
				t.Fatalf("after saving: LAN %+v, %v; want %+v", again.LAN, err, tt.want)
			}
		})
	}
}

// With the app lock on, the password of CryptBridge 0.5's one access is
// sealed apart from its other settings; unlocking must give it to the access
// that took the old one's place.
func TestConvertLockedLANOfVersion05(t *testing.T) {
	for _, allowChanges := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "settings.dat")
		writeOlderFormat(t, path, headerVersion2, map[string]any{
			"appLock": true,
			"sealed": sealForTest(t, "app password for the test",
				map[string]any{"lanPassword": "old-sealed-password", "vaultKeys": map[string]any{}}),
			"lan": lanVersion1{Enabled: true, Port: 8080, User: "tv", AllowChanges: allowChanges},
		})

		got, gotKey, err := Unlock(path, `C:\cache`, "app password for the test")
		if err != nil {
			t.Fatal(err)
		}
		kept, other := got.LAN.Streaming, got.LAN.Full
		if allowChanges {
			kept, other = got.LAN.Full, got.LAN.Streaming
		}
		if kept != (LANAccess{Enabled: true, Port: 8080, User: "tv", Password: "old-sealed-password"}) || other.Password != "" {
			t.Fatalf("allow changes %v: LAN %+v; want the old access with its sealed password", allowChanges, got.LAN)
		}
		if len(got.Accounts) != 0 {
			t.Fatalf("allow changes %v: accounts %+v; want none (there was no pCloud sign-in)", allowChanges, got.Accounts)
		}

		// Saved again, the password is sealed under the new name.
		if err := Save(path, got, gotKey); err != nil {
			t.Fatal(err)
		}
		again, _, err := Unlock(path, `C:\cache`, "app password for the test")
		if err != nil || again.LAN != got.LAN {
			t.Fatalf("allow changes %v: after saving: LAN %+v, %v; want %+v", allowChanges, again.LAN, err, got.LAN)
		}
	}
}
