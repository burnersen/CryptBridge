// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package settings stores everything CryptBridge needs to remember. The
// whole file is encrypted with Windows DPAPI, bound to the user's Windows
// account: the pCloud sign-in and the vault keys never touch the disk in a
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
	"os"
	"path/filepath"
	"slices"

	"github.com/burnersen/CryptBridge/windows/internal/passlock"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// fileHeader marks the file format. Version 2 holds a list of vaults;
// version 1 (CryptBridge 0.1 to 0.3) held exactly one. The header changed so
// that an older CryptBridge refuses the new file instead of reading it
// without the vault list and losing the list with its next save.
var (
	fileHeader   = []byte("CryptBridge-Settings-2\n")
	legacyHeader = []byte("CryptBridge-Settings-1\n")
)

// LegacyBackupSuffix is added to the name of the settings file for the copy
// of a version 1 file that is kept when it is converted.
const LegacyBackupSuffix = ".v1-backup"

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
	KindPCloud = "pcloud" // a folder in the pCloud account
	KindLocal  = "local"  // a folder on this PC or on a drive plugged into it
)

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
	ID          string       `json:"id"`               // never changes; names the vault in rclone and its cache folder
	Name        string       `json:"name"`             // shown in the window and in Explorer
	Kind        string       `json:"kind"`             // KindPCloud or KindLocal
	Letter      string       `json:"letter"`           // drive letter without colon
	AutoConnect bool         `json:"autoConnect"`      // connect when CryptBridge starts (local: also when the folder appears)
	Folder      *FolderMark  `json:"folder,omitempty"` // local: the folder the drive was last connected to
	LastSize    *SizeRecord  `json:"lastSize,omitempty"`
	Config      vault.Config `json:"config"` // location and keys: protected by the app lock
}

// HasKeys reports whether the location and keys of the vault are known.
func (v Vault) HasKeys() bool {
	return v.Config.Password != "" && v.Config.NormalisedPath() != ""
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
	PCloud        pcloud.Account `json:"pcloud"`
	PCloudApp     pcloud.App     `json:"pcloudApp"`   // empty: rclone's built-in app
	SignInAppID   string         `json:"signInAppId"` // client ID the sign-in was made with; empty: rclone's app
	Vaults        []Vault        `json:"vaults"`
	Cache         Cache          `json:"cache"`
	AutoReconnect bool           `json:"autoReconnect"` // reconnect after a drive was lost unexpectedly
	Theme         string         `json:"theme"`         // ThemeAuto, ThemeLight or ThemeDark
	AppLock       bool           `json:"appLock"`       // the secrets are sealed with the app password
	LockOffered   bool           `json:"lockOffered"`   // the app password was offered once (set up or declined)
	LAN           LAN            `json:"lan"`
}

// Clone returns a copy that can be changed without changing s: the vault
// list is copied, not shared.
func (s Settings) Clone() Settings {
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

// secrets are the parts of the settings that the app password protects.
// Everything else (vault names and letters, behaviour) stays readable, so
// that a locked CryptBridge can still show its window and tidy up.
type secrets struct {
	PCloud      pcloud.Account          `json:"pcloud"`
	PCloudApp   pcloud.App              `json:"pcloudApp"`
	SignInAppID string                  `json:"signInAppId"`
	VaultKeys   map[string]vault.Config `json:"vaultKeys"`       // by vault ID
	Vault       *vault.Config           `json:"vault,omitempty"` // only in version 1 files
	// The passwords of the LAN accesses: they open the drives from the network.
	LANStreamingPassword string `json:"lanStreamingPassword"`
	LANFullPassword      string `json:"lanFullPassword"`
	LANPassword          string `json:"lanPassword,omitempty"` // only from CryptBridge 0.5: its one access
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

	// lanVersion1Full: the file came from CryptBridge 0.5, and its one LAN
	// access allowed changes. Its sealed password belongs to the full access.
	lanVersion1Full bool
}

// ErrNeedsKey: settings with the app lock on can only be saved with the key
// derived from the app password.
var ErrNeedsKey = errors.New("the settings are locked with the app password")

// WithoutSecrets returns the settings with sign-in, vault locations, vault
// keys and the LAN passwords removed: what a locked CryptBridge knows.
func (s Settings) WithoutSecrets() Settings {
	s = s.Clone()
	s.PCloud = pcloud.Account{}
	s.PCloudApp = pcloud.App{}
	s.SignInAppID = ""
	for i := range s.Vaults {
		s.Vaults[i].Config = vault.Config{}
	}
	s.LAN.Streaming.Password = ""
	s.LAN.Full.Password = ""
	return s
}

func (s Settings) secrets() secrets {
	keys := make(map[string]vault.Config, len(s.Vaults))
	for _, v := range s.Vaults {
		keys[v.ID] = v.Config
	}
	return secrets{
		PCloud:               s.PCloud,
		PCloudApp:            s.PCloudApp,
		SignInAppID:          s.SignInAppID,
		VaultKeys:            keys,
		LANStreamingPassword: s.LAN.Streaming.Password,
		LANFullPassword:      s.LAN.Full.Password,
	}
}

// withSecrets puts the secrets back. A vault whose keys are not among them
// is dropped: without location and keys it could never be opened, and the
// placeholder for the vault of a locked version 1 file may stand for no
// vault at all.
func (s Settings) withSecrets(sec secrets) Settings {
	s = s.Clone()
	s.PCloud = sec.PCloud
	s.PCloudApp = sec.PCloudApp
	s.SignInAppID = sec.SignInAppID
	s.LAN.Streaming.Password = sec.LANStreamingPassword
	s.LAN.Full.Password = sec.LANFullPassword
	kept := s.Vaults[:0]
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
			kept = append(kept, v)
		}
	}
	s.Vaults = kept
	return s
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

// SignedIn reports whether a pCloud sign-in is stored.
func (s Settings) SignedIn() bool {
	return s.PCloud.Token != "" && s.PCloud.Hostname != ""
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

// IsLegacy reports whether the file at path is a version 1 file, which is
// converted the next time the settings are saved.
func IsLegacy(path string) bool {
	head := make([]byte, len(legacyHeader))
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	n, _ := f.Read(head)
	return bytes.Equal(head[:n], legacyHeader)
}

// RemoveLegacyBackup deletes the copy of a version 1 file. It holds the
// secrets protected as they were back then, so it goes when their
// protection changes (app password set, changed or reset).
func RemoveLegacyBackup(path string) error {
	err := os.Remove(path + LegacyBackupSuffix)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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

// readFile reads and decrypts the DPAPI layer, converting a version 1 file.
func readFile(path, defaultCacheDir string) (fileFormat, error) {
	stored := fileFormat{Settings: Defaults(defaultCacheDir)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return stored, nil
	}
	if err != nil {
		return stored, fmt.Errorf("read settings: %w", err)
	}
	header := fileHeader
	if bytes.HasPrefix(data, legacyHeader) {
		header = legacyHeader
	} else if !bytes.HasPrefix(data, fileHeader) {
		return stored, errors.New("settings file has an unknown format (was it written by a newer CryptBridge?)")
	}
	plain, err := unprotect(data[len(header):])
	if err != nil {
		return stored, fmt.Errorf("decrypt settings (was the file copied from another Windows account?): %w", err)
	}
	defer clear(plain)
	if bytes.Equal(header, legacyHeader) {
		converted, err := fromVersion1(plain, defaultCacheDir)
		if err != nil {
			return fileFormat{Settings: Defaults(defaultCacheDir)}, fmt.Errorf("read settings: %w", err)
		}
		return converted, nil
	}
	if err := json.Unmarshal(plain, &stored); err != nil {
		return fileFormat{Settings: Defaults(defaultCacheDir)}, fmt.Errorf("read settings: %w", err)
	}
	if old, found := lanVersion1Of(plain); found {
		stored.LAN = old.convert()
		stored.lanVersion1Full = old.AllowChanges
	}
	return stored, nil
}

// fileVersion1 is the content of a version 1 file (CryptBridge 0.1 to 0.3).
type fileVersion1 struct {
	PCloud      pcloud.Account `json:"pcloud"`
	PCloudApp   pcloud.App     `json:"pcloudApp"`
	SignInAppID string         `json:"signInAppId"`
	Vault       vault.Config   `json:"vault"`
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

// fromVersion1 converts a version 1 file: its single vault becomes the first
// vault of the list, with the drive letter and "connect on start" it had.
// With the app lock on, the vault keys are still sealed; the vault is listed
// without them, and Unlock fills them in from the sealed part.
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
		PCloud:      old.PCloud,
		PCloudApp:   old.PCloudApp,
		SignInAppID: old.SignInAppID,
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
		Kind:        KindPCloud,
		Letter:      old.Drive.Letter,
		AutoConnect: old.AutoConnect,
		Config:      old.Vault,
	}
	if migrated.HasKeys() || (old.AppLock && old.Sealed != nil) {
		converted.Vaults = []Vault{migrated}
	}
	return fileFormat{Settings: converted, Sealed: old.Sealed}, nil
}

// Save encrypts and writes the settings. With the app lock on, key must be
// the key from Unlock or passlock.NewKey: the secrets are sealed with it and
// never written readable. The old file is replaced only after the new one is
// completely on disk; a version 1 file is kept as a backup first.
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

// backUpLegacyFile copies a version 1 file before it is replaced, once: if
// the conversion ever lost a vault, its keys are still in the copy. An
// existing copy is never overwritten.
func backUpLegacyFile(path string) error {
	if !IsLegacy(path) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("back up the old settings: %w", err)
	}
	backup, err := os.OpenFile(path+LegacyBackupSuffix, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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
