// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package settings stores everything CryptBridge needs to remember. The
// whole file is encrypted with Windows DPAPI, bound to the user's Windows
// account: the cloud sign-ins and the vault keys never touch the disk in a
// readable form.
//
// With the app lock on, the secrets are sealed once more with the user's
// app password: DPAPI opens the file for anyone signed in to the Windows
// account, the app password only for the user.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/burnersen/CryptBridge/windows/internal/cloud"
	"github.com/burnersen/CryptBridge/windows/internal/passlock"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// fileHeader marks the file format. Version 3 holds a list of cloud
// accounts; version 2 (CryptBridge 0.4 to 0.6) held one pCloud account and a
// list of vaults; version 1 (CryptBridge 0.1 to 0.3) held exactly one vault.
// The header changes with the format so that an older CryptBridge refuses
// the new file instead of reading it without the new parts and losing them
// with its next save.
var fileHeader = []byte("CryptBridge-Settings-3\n")

// olderFormats are the headers of earlier formats, which are converted when
// read, and the suffix of the copy kept when such a file is replaced.
var olderFormats = []struct {
	header       []byte
	backupSuffix string
}{
	{[]byte("CryptBridge-Settings-1\n"), ".v1-backup"},
	{[]byte("CryptBridge-Settings-2\n"), ".v2-backup"},
}

// version1Header marks the format of CryptBridge 0.1 to 0.3.
var version1Header = olderFormats[0].header

// Default sizes of the drive cache.
const (
	DefaultCacheMaxSize      = "20G" // the project's required cache size
	DefaultCacheMinFreeSpace = "10G" // emergency brake for the system disk
	DefaultDriveLetter       = "Z"
)

// Defaults of the two LAN accesses: ports next to each other, and user names
// that tell which access is which and are easy to type with a TV remote.
const (
	DefaultStreamingPort = 8080
	DefaultStreamingUser = "stream"
	DefaultFullPort      = 8081
	DefaultFullUser      = "full"
)

// Where a vault lives.
const (
	KindCloud = "cloud" // a folder in one of the cloud accounts
	KindLocal = "local" // a folder on this PC or on a drive plugged into it
)

// kindPCloud marked the vaults in the one pCloud account of version 2 files.
const kindPCloud = "pcloud"

// MigratedAccountID is the ID of the pCloud account taken over from a
// version 1 or 2 file. Its rclone remote keeps the name it had ("cbpcloud").
const MigratedAccountID = "pcloud"

// migratedAccountName is how the taken-over pCloud account is called.
const migratedAccountName = "pCloud"

// Window themes.
const (
	ThemeAuto  = "auto" // like Windows
	ThemeLight = "light"
	ThemeDark  = "dark"
)

// MigratedVaultID is the ID of the vault taken over from a version 1 file.
// It keeps the name of its rclone remote ("cbvault"): rclone names the cache
// folder after the remote, and files still waiting for upload must be found
// there again.
const MigratedVaultID = "vault"

// migratedVaultName is how the taken-over vault is called: Explorer showed
// its drive as "Vault" before.
const migratedVaultName = "Vault"

// Cache holds the settings of the cache that all drives share.
type Cache struct {
	Dir          string `json:"dir"`
	MaxSize      string `json:"maxSize"`      // per drive; a soft limit, files in use stay
	MinFreeSpace string `json:"minFreeSpace"` // emergency brake: keep this much disk space free
}

// SizeRecord is the last measured size of a vault, kept because measuring a
// large vault in pCloud takes minutes.
type SizeRecord struct {
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
	At    string `json:"at"` // RFC 3339
}

// FolderMark identifies the folder of a local vault: the serial number of
// its disk and the folder's number on that disk. A copy of the vault on
// another USB drive has the same path and keys, but another mark.
type FolderMark struct {
	Volume uint32 `json:"volume"`
	Index  uint64 `json:"index"`
}

// Vault is one vault and its drive.
type Vault struct {
	ID          string       `json:"id"`                // never changes; names the vault in rclone and its cache folder
	Name        string       `json:"name"`              // shown in the window and in Explorer
	Kind        string       `json:"kind"`              // KindCloud or KindLocal
	Account     string       `json:"account,omitempty"` // KindCloud: ID of the account that holds the folder
	Letter      string       `json:"letter"`            // drive letter without colon
	AutoConnect bool         `json:"autoConnect"`       // connect when CryptBridge starts (local: also when the folder appears)
	Folder      *FolderMark  `json:"folder,omitempty"`  // local: the folder the drive was last connected to
	LastSize    *SizeRecord  `json:"lastSize,omitempty"`
	Config      vault.Config `json:"config"` // location and keys: protected by the app lock
}

// HasKeys reports whether the location and keys of the vault are known.
func (v Vault) HasKeys() bool {
	return v.Config.Password != "" && v.Config.NormalisedPath() != ""
}

// Account is a sign-in at a cloud service. There can be several accounts of
// the same service.
type Account struct {
	ID      string `json:"id"`      // never changes; names the account in rclone
	Service string `json:"service"` // rclone's backend name, e.g. cloud.Drive
	Name    string `json:"name"`    // shown in the window
	// The secrets, protected by the app lock:
	App    cloud.App         `json:"app"`              // the user's own app; empty: rclone's
	Remote map[string]string `json:"remote,omitempty"` // rclone's settings of the account, the token among them; empty while signed out
}

// SignedIn reports whether a sign-in is stored for the account.
func (a Account) SignedIn() bool {
	return a.Remote["token"] != ""
}

// LANAccess is one way for the other devices of the home network into the
// connected drives: a WebDAV server on a port of its own, with its own user
// name and password.
type LANAccess struct {
	Enabled  bool   `json:"enabled"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"` // protected by the app lock
}

// LAN holds the two accesses of the home network. They are separate so that
// the password typed on TVs cannot change or delete anything.
type LAN struct {
	Streaming LANAccess `json:"streaming"` // read only: for video players and TVs
	Full      LANAccess `json:"full"`      // read, write and delete: for file apps
}

// defaultLAN returns both accesses switched off, on their default ports.
func defaultLAN() LAN {
	return LAN{
		Streaming: LANAccess{Port: DefaultStreamingPort, User: DefaultStreamingUser},
		Full:      LANAccess{Port: DefaultFullPort, User: DefaultFullUser},
	}
}

// lanVersion1 is the LAN access of CryptBridge 0.5: one server, whose rights
// depended on a switch.
type lanVersion1 struct {
	Enabled      bool   `json:"enabled"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	Password     string `json:"password"`
	AllowChanges bool   `json:"allowChanges"`
}

// lanVersion1Of finds the LAN settings of CryptBridge 0.5 in the content of a
// settings file. They are recognised by a port directly in the LAN settings,
// where newer settings hold the two accesses.
func lanVersion1Of(plain []byte) (lanVersion1, bool) {
	var file struct {
		LAN json.RawMessage `json:"lan"`
	}
	if json.Unmarshal(plain, &file) != nil || len(file.LAN) == 0 {
		return lanVersion1{}, false
	}
	var probe struct {
		Port *int `json:"port"`
	}
	if json.Unmarshal(file.LAN, &probe) != nil || probe.Port == nil {
		return lanVersion1{}, false
	}
	var old lanVersion1
	if json.Unmarshal(file.LAN, &old) != nil {
		return lanVersion1{}, false
	}
	return old, true
}

// convert turns the one access of CryptBridge 0.5 into the access with the
// same rights, unchanged, so that the devices that use it keep working. The
// other access stays off; if its default port is the one taken over, it gets
// the other default port.
func (old lanVersion1) convert() LAN {
	converted := defaultLAN()
	kept := LANAccess{Enabled: old.Enabled, Port: old.Port, User: old.User, Password: old.Password}
	if old.AllowChanges {
		converted.Full = kept
		if converted.Streaming.Port == kept.Port {
			converted.Streaming.Port = DefaultFullPort
		}
	} else {
		converted.Streaming = kept
		if converted.Full.Port == kept.Port {
			converted.Full.Port = DefaultStreamingPort
		}
	}
	return converted
}

// Settings is everything CryptBridge remembers.
type Settings struct {
	Accounts      []Account `json:"accounts"`
	Vaults        []Vault   `json:"vaults"`
	Cache         Cache     `json:"cache"`
	AutoReconnect bool      `json:"autoReconnect"` // reconnect after a drive was lost unexpectedly
	Theme         string    `json:"theme"`         // ThemeAuto, ThemeLight or ThemeDark
	AppLock       bool      `json:"appLock"`       // the secrets are sealed with the app password
	LockOffered   bool      `json:"lockOffered"`   // the app password was offered once (set up or declined)
	LAN           LAN       `json:"lan"`
}

// Clone returns a copy that can be changed without changing s: the account
// and vault lists are copied, not shared.
func (s Settings) Clone() Settings {
	s.Accounts = slices.Clone(s.Accounts)
	for i := range s.Accounts {
		s.Accounts[i].Remote = maps.Clone(s.Accounts[i].Remote)
	}
	s.Vaults = slices.Clone(s.Vaults)
	for i := range s.Vaults {
		if record := s.Vaults[i].LastSize; record != nil {
			copied := *record
			s.Vaults[i].LastSize = &copied
		}
		if mark := s.Vaults[i].Folder; mark != nil {
			copied := *mark
			s.Vaults[i].Folder = &copied
		}
	}
	return s
}

// Vault returns the vault with the given ID.
func (s Settings) Vault(id string) (Vault, bool) {
	for _, v := range s.Vaults {
		if v.ID == id {
			return v, true
		}
	}
	return Vault{}, false
}

// Account returns the account with the given ID.
func (s Settings) Account(id string) (Account, bool) {
	for _, a := range s.Accounts {
		if a.ID == id {
			return a, true
		}
	}
	return Account{}, false
}

// secrets are the parts of the settings that the app password protects.
// Everything else (account and vault names, letters, behaviour) stays
// readable, so that a locked CryptBridge can still show its window and tidy
// up.
type secrets struct {
	Accounts  map[string]accountSecrets `json:"accounts"`        // by account ID
	VaultKeys map[string]vault.Config   `json:"vaultKeys"`       // by vault ID
	Vault     *vault.Config             `json:"vault,omitempty"` // only in version 1 files
	// The passwords of the LAN accesses: they open the drives from the network.
	LANStreamingPassword string `json:"lanStreamingPassword"`
	LANFullPassword      string `json:"lanFullPassword"`
	LANPassword          string `json:"lanPassword,omitempty"` // only from CryptBridge 0.5: its one access
	// The one pCloud sign-in of version 1 and 2 files.
	PCloud      *pcloud.Account `json:"pcloud,omitempty"`
	PCloudApp   *cloud.App      `json:"pcloudApp,omitempty"`
	SignInAppID string          `json:"signInAppId,omitempty"`
}

// accountSecrets are the secrets of one account.
type accountSecrets struct {
	App    cloud.App         `json:"app"`
	Remote map[string]string `json:"remote,omitempty"`
}

// upgradeLAN gives the password of CryptBridge 0.5's one access to the access
// that took its place (see lanVersion1.convert).
func (sec *secrets) upgradeLAN(wasFull bool) {
	if sec.LANPassword == "" {
		return
	}
	if wasFull {
		sec.LANFullPassword = sec.LANPassword
	} else {
		sec.LANStreamingPassword = sec.LANPassword
	}
	sec.LANPassword = ""
}

// fileFormat is what is stored inside the DPAPI layer: the settings, and
// with the app lock on, the sealed secrets instead of the readable ones.
type fileFormat struct {
	Settings
	Sealed *passlock.Sealed `json:"sealed,omitempty"`

	// The one pCloud sign-in of version 1 and 2 files; convertVersion2 moves
	// it into the account list. Empty in the current format.
	PCloud      *pcloud.Account `json:"pcloud,omitempty"`
	PCloudApp   *cloud.App      `json:"pcloudApp,omitempty"`
	SignInAppID string          `json:"signInAppId,omitempty"` // client ID the sign-in was made with; empty: rclone's app

	// lanVersion1Full: the file came from CryptBridge 0.5, and its one LAN
	// access allowed changes. Its sealed password belongs to the full access.
	lanVersion1Full bool
}

// ErrNeedsKey: settings with the app lock on can only be saved with the key
// derived from the app password.
var ErrNeedsKey = errors.New("the settings are locked with the app password")

// WithoutSecrets returns the settings with sign-ins, own apps, vault
// locations, vault keys and the LAN passwords removed: what a locked
// CryptBridge knows.
func (s Settings) WithoutSecrets() Settings {
	s = s.Clone()
	for i := range s.Accounts {
		s.Accounts[i].App = cloud.App{}
		s.Accounts[i].Remote = nil
	}
	for i := range s.Vaults {
		s.Vaults[i].Config = vault.Config{}
	}
	s.LAN.Streaming.Password = ""
	s.LAN.Full.Password = ""
	return s
}

func (s Settings) secrets() secrets {
	accounts := make(map[string]accountSecrets, len(s.Accounts))
	for _, a := range s.Accounts {
		accounts[a.ID] = accountSecrets{App: a.App, Remote: a.Remote}
	}
	keys := make(map[string]vault.Config, len(s.Vaults))
	for _, v := range s.Vaults {
		keys[v.ID] = v.Config
	}
	return secrets{
		Accounts:             accounts,
		VaultKeys:            keys,
		LANStreamingPassword: s.LAN.Streaming.Password,
		LANFullPassword:      s.LAN.Full.Password,
	}
}

// withSecrets puts the secrets back. A vault whose keys are not among them
// is dropped: without location and keys it could never be opened, and the
// placeholder for the vault of a locked version 1 file may stand for no
// vault at all. Likewise, the placeholder for the pCloud account of a locked
// version 1 or 2 file is dropped if there was no such account.
func (s Settings) withSecrets(sec secrets) Settings {
	s = s.Clone()
	s.LAN.Streaming.Password = sec.LANStreamingPassword
	s.LAN.Full.Password = sec.LANFullPassword

	keptVaults := s.Vaults[:0]
	for _, v := range s.Vaults {
		keys, found := sec.VaultKeys[v.ID]
		if !found && v.ID == MigratedVaultID && sec.Vault != nil {
			keys, found = *sec.Vault, true
		}
		if !found {
			continue
		}
		v.Config = keys
		if v.HasKeys() {
			keptVaults = append(keptVaults, v)
		}
	}
	s.Vaults = keptVaults

	keptAccounts := s.Accounts[:0]
	for _, a := range s.Accounts {
		own, found := sec.Accounts[a.ID]
		if !found && a.ID == MigratedAccountID {
			own.App, own.Remote = pcloudSecrets(sec.PCloud, sec.PCloudApp, sec.SignInAppID)
			found = own.Remote != nil || own.App.IsSet() || s.usesAccount(a.ID)
		}
		if !found {
			continue
		}
		a.App, a.Remote = own.App, own.Remote
		keptAccounts = append(keptAccounts, a)
	}
	s.Accounts = keptAccounts
	return s
}

// usesAccount reports whether a vault lies in the account with the given ID.
func (s Settings) usesAccount(id string) bool {
	return slices.ContainsFunc(s.Vaults, func(v Vault) bool { return v.Kind == KindCloud && v.Account == id })
}

// pcloudSecrets turns the one pCloud sign-in of version 1 and 2 files into
// the secrets of an account: rclone's settings of the remote as CryptBridge
// 0.6 registered it (hostname and token), and the own app. The client ID
// the sign-in was made with is kept with it, as rclone keeps it for the
// other services.
func pcloudSecrets(account *pcloud.Account, app *cloud.App, signInAppID string) (cloud.App, map[string]string) {
	var own cloud.App
	if app != nil {
		own = *app
	}
	if account == nil || account.Token == "" {
		return own, nil
	}
	remote := map[string]string{"type": cloud.PCloud, "hostname": account.Hostname, "token": account.Token}
	if signInAppID != "" {
		remote["client_id"] = signInAppID
		if signInAppID == own.ClientID {
			remote["client_secret"] = own.ClientSecret
		}
	}
	return own, remote
}

// Defaults returns the settings of a fresh installation.
func Defaults(cacheDir string) Settings {
	return Settings{
		Cache: Cache{
			Dir:          cacheDir,
			MaxSize:      DefaultCacheMaxSize,
			MinFreeSpace: DefaultCacheMinFreeSpace,
		},
		AutoReconnect: true,
		Theme:         ThemeAuto,
		LAN:           defaultLAN(),
	}
}

// Load reads the settings. A missing file is not an error: it means a fresh
// installation, and the defaults are returned. With the app lock on, the
// result has no secrets; Unlock opens them.
func Load(path, defaultCacheDir string) (Settings, error) {
	stored, err := readFile(path, defaultCacheDir)
	if stored.AppLock {
		// A locked file holds no readable secrets anyway; this keeps the
		// promise even for a damaged one.
		return stored.Settings.WithoutSecrets(), err
	}
	return stored.Settings, err
}

// Unlock reads the settings and opens the secrets with the app password.
// The returned key seals later changes (see Save); wipe it when locking.
func Unlock(path, defaultCacheDir, password string) (Settings, *passlock.Key, error) {
	stored, err := readFile(path, defaultCacheDir)
	if err != nil {
		return Settings{}, nil, err
	}
	if !stored.AppLock {
		return stored.Settings, nil, errors.New("CryptBridge is not locked with an app password")
	}
	if stored.Sealed == nil {
		return Settings{}, nil, errors.New("the locked settings are damaged")
	}
	plain, key, err := passlock.Open(*stored.Sealed, password)
	if err != nil {
		return Settings{}, nil, err
	}
	var sec secrets
	err = json.Unmarshal(plain, &sec)
	clear(plain)
	if err != nil {
		key.Wipe()
		return Settings{}, nil, fmt.Errorf("read locked settings: %w", err)
	}
	sec.upgradeLAN(stored.lanVersion1Full)
	return stored.Settings.withSecrets(sec), key, nil
}

// IsLegacy reports whether the file at path has an older format, which is
// converted the next time the settings are saved.
func IsLegacy(path string) bool {
	_, found := olderFormatOf(path)
	return found
}

// olderFormatOf returns the index in olderFormats of the file's format.
func olderFormatOf(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	head := make([]byte, len(fileHeader))
	n, _ := io.ReadFull(f, head)
	for i, format := range olderFormats {
		if bytes.Equal(head[:n], format.header) {
			return i, true
		}
	}
	return 0, false
}

// RemoveLegacyBackups deletes the copies of files in older formats. They
// hold the secrets protected as they were back then, so they go when their
// protection changes (app password set, changed or reset).
func RemoveLegacyBackups(path string) error {
	for _, format := range olderFormats {
		backup := path + format.backupSuffix
		if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete %s: %w", backup, err)
		}
	}
	return nil
}

// SetAside renames a settings file that could not be read, so that setting
// CryptBridge up again does not overwrite it: it may come from a newer
// version, or be readable again later. It returns the new name.
func SetAside(path string) (string, error) {
	for n := 1; n <= maxSetAside; n++ {
		target := fmt.Sprintf("%s.unreadable-%d", path, n)
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.Rename(path, target); err != nil {
			return "", fmt.Errorf("keep the unreadable settings: %w", err)
		}
		return target, nil
	}
	return "", fmt.Errorf("keep the unreadable settings: %d older copies are in the way", maxSetAside)
}

// maxSetAside limits the number of unreadable settings files kept.
const maxSetAside = 20

// readFile reads and decrypts the DPAPI layer, converting files of older
// formats.
func readFile(path, defaultCacheDir string) (fileFormat, error) {
	stored := fileFormat{Settings: Defaults(defaultCacheDir)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return stored, nil
	}
	if err != nil {
		return stored, fmt.Errorf("read settings: %w", err)
	}
	header, known := headerOf(data)
	if !known {
		return stored, errors.New("settings file has an unknown format (was it written by a newer CryptBridge?)")
	}
	plain, err := unprotect(data[len(header):])
	if err != nil {
		return stored, fmt.Errorf("decrypt settings (was the file copied from another Windows account?): %w", err)
	}
	defer clear(plain)
	if bytes.Equal(header, version1Header) {
		stored, err = fromVersion1(plain, defaultCacheDir)
	} else {
		err = json.Unmarshal(plain, &stored)
	}
	if err != nil {
		return fileFormat{Settings: Defaults(defaultCacheDir)}, fmt.Errorf("read settings: %w", err)
	}
	if old, found := lanVersion1Of(plain); found {
		stored.LAN = old.convert()
		stored.lanVersion1Full = old.AllowChanges
	}
	if !bytes.Equal(header, fileHeader) {
		stored.convertVersion2()
	}
	return stored, nil
}

// headerOf returns the header the file content starts with, if it is one of
// the known formats.
func headerOf(data []byte) ([]byte, bool) {
	if bytes.HasPrefix(data, fileHeader) {
		return fileHeader, true
	}
	for _, format := range olderFormats {
		if bytes.HasPrefix(data, format.header) {
			return format.header, true
		}
	}
	return nil, false
}

// convertVersion2 turns the one pCloud account of version 1 and 2 files
// into the first entry of the account list, and the pCloud vaults into
// vaults of that account. With the app lock on, the sign-in is still
// sealed: the account is listed without it, and Unlock fills it in, or
// drops the account if there was none.
func (f *fileFormat) convertVersion2() {
	usesPCloud := false
	for i := range f.Vaults {
		if f.Vaults[i].Kind == kindPCloud {
			f.Vaults[i].Kind = KindCloud
			f.Vaults[i].Account = MigratedAccountID
			usesPCloud = true
		}
	}
	account := Account{ID: MigratedAccountID, Service: cloud.PCloud, Name: migratedAccountName}
	account.App, account.Remote = pcloudSecrets(f.PCloud, f.PCloudApp, f.SignInAppID)
	if usesPCloud || f.AppLock || account.SignedIn() || account.App.IsSet() {
		f.Accounts = append([]Account{account}, f.Accounts...)
	}
	f.PCloud, f.PCloudApp, f.SignInAppID = nil, nil, ""
}

// fileVersion1 is the content of a version 1 file (CryptBridge 0.1 to 0.3).
type fileVersion1 struct {
	PCloud      *pcloud.Account `json:"pcloud"`
	PCloudApp   *cloud.App      `json:"pcloudApp"`
	SignInAppID string          `json:"signInAppId"`
	Vault       vault.Config    `json:"vault"`
	Drive       struct {
		Letter            string `json:"letter"`
		CacheDir          string `json:"cacheDir"`
		CacheMaxSize      string `json:"cacheMaxSize"`
		CacheMinFreeSpace string `json:"cacheMinFreeSpace"`
	} `json:"drive"`
	AutoConnect   bool             `json:"autoConnect"`
	AutoReconnect bool             `json:"autoReconnect"`
	AppLock       bool             `json:"appLock"`
	LockOffered   bool             `json:"lockOffered"`
	Sealed        *passlock.Sealed `json:"sealed,omitempty"`
}

// fromVersion1 converts a version 1 file into the content of a version 2
// file: its single vault becomes the first vault of the list, with the drive
// letter and "connect on start" it had. With the app lock on, the vault keys
// are still sealed; the vault is listed without them, and Unlock fills them
// in from the sealed part.
func fromVersion1(plain []byte, defaultCacheDir string) (fileFormat, error) {
	defaults := Defaults(defaultCacheDir)
	var old fileVersion1
	old.Drive.Letter = DefaultDriveLetter
	old.Drive.CacheDir = defaults.Cache.Dir
	old.Drive.CacheMaxSize = defaults.Cache.MaxSize
	old.Drive.CacheMinFreeSpace = defaults.Cache.MinFreeSpace
	old.AutoConnect = true
	old.AutoReconnect = defaults.AutoReconnect
	if err := json.Unmarshal(plain, &old); err != nil {
		return fileFormat{}, err
	}

	converted := Settings{
		Cache: Cache{
			Dir:          old.Drive.CacheDir,
			MaxSize:      old.Drive.CacheMaxSize,
			MinFreeSpace: old.Drive.CacheMinFreeSpace,
		},
		AutoReconnect: old.AutoReconnect,
		Theme:         ThemeAuto,
		AppLock:       old.AppLock,
		LockOffered:   old.LockOffered,
		LAN:           defaults.LAN,
	}
	migrated := Vault{
		ID:          MigratedVaultID,
		Name:        migratedVaultName,
		Kind:        kindPCloud,
		Letter:      old.Drive.Letter,
		AutoConnect: old.AutoConnect,
		Config:      old.Vault,
	}
	if migrated.HasKeys() || (old.AppLock && old.Sealed != nil) {
		converted.Vaults = []Vault{migrated}
	}
	return fileFormat{
		Settings:    converted,
		Sealed:      old.Sealed,
		PCloud:      old.PCloud,
		PCloudApp:   old.PCloudApp,
		SignInAppID: old.SignInAppID,
	}, nil
}

// Save encrypts and writes the settings. With the app lock on, key must be
// the key from Unlock or passlock.NewKey: the secrets are sealed with it and
// never written readable. The old file is replaced only after the new one is
// completely on disk; a file of an older format is kept as a backup first.
func Save(path string, s Settings, key *passlock.Key) error {
	stored := fileFormat{Settings: s}
	if s.AppLock {
		if key == nil {
			return ErrNeedsKey
		}
		secretJSON, err := json.Marshal(s.secrets())
		if err != nil {
			return fmt.Errorf("encode settings: %w", err)
		}
		sealed, err := key.Seal(secretJSON)
		clear(secretJSON)
		if err != nil {
			return fmt.Errorf("lock settings: %w", err)
		}
		stored = fileFormat{Settings: s.WithoutSecrets(), Sealed: &sealed}
	}
	plain, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	protected, err := protect(plain)
	clear(plain)
	if err != nil {
		return fmt.Errorf("encrypt settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings folder: %w", err)
	}
	if err := backUpLegacyFile(path); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(append([]byte{}, fileHeader...), protected...), 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// backUpLegacyFile copies a file of an older format before it is replaced,
// once: if the conversion ever lost an account or a vault, its secrets are
// still in the copy. An existing copy is never overwritten.
func backUpLegacyFile(path string) error {
	format, found := olderFormatOf(path)
	if !found {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("back up the old settings: %w", err)
	}
	backup, err := os.OpenFile(path+olderFormats[format].backupSuffix, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("back up the old settings: %w", err)
	}
	if _, err := backup.Write(data); err != nil {
		backup.Close()
		return fmt.Errorf("back up the old settings: %w", err)
	}
	if err := backup.Close(); err != nil {
		return fmt.Errorf("back up the old settings: %w", err)
	}
	return nil
}
