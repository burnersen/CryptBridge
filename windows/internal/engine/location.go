// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/burnersen/CryptBridge/windows/internal/appdir"
	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// remotePrefix starts the names of the remotes in rclone's in-memory
// configuration. A vault's remote is the prefix plus its ID ("cbvault" for
// the vault taken over from CryptBridge 0.3), and rclone names the vault's
// cache folder after it. An account's remote is the prefix plus the
// account's ID ("cbpcloud" for the account taken over from CryptBridge 0.6).
// The IDs cannot collide: vault IDs start with "v", new account IDs with "a".
const remotePrefix = "cb"

// maxNameLength keeps vault names short enough for Explorer.
const maxNameLength = 32

// forbiddenNameChars cannot be part of a drive's name in Explorer.
const forbiddenNameChars = `\/:*?"<>|`

var errUnknownVault = errors.New("this vault is not known (was it removed?)")

func remoteName(id string) string { return remotePrefix + id }

// remoteOf returns the rclone remote of a vault, e.g. "cbvault:".
func remoteOf(v settings.Vault) string { return remoteName(v.ID) + ":" }

// localPath returns the folder of a local vault as Windows writes it.
func localPath(v settings.Vault) string {
	return filepath.Clean(v.Config.Path)
}

// location is how the window shows where a vault is.
func location(v settings.Vault) string {
	if v.Config.Path == "" {
		return ""
	}
	if v.Kind == settings.KindLocal {
		return localPath(v)
	}
	return v.Config.NormalisedPath()
}

// placeOf returns the remote a vault lives on and its configuration with the
// folder relative to that remote: the account's remote ("cbpcloud:") and the
// folder in the cloud, or the drive ("E:/") and the folder on it. Keeping
// the drive letter out of the folder keeps the check code of a USB drive the
// same under any letter.
func placeOf(v settings.Vault) (string, vault.Config) {
	if v.Kind != settings.KindLocal {
		return remoteName(v.Account) + ":", v.Config
	}
	return splitLocal(v.Config)
}

// splitLocal splits the folder of a local vault into drive and path.
func splitLocal(cfg vault.Config) (string, vault.Config) {
	clean := filepath.Clean(cfg.Path)
	volume := filepath.VolumeName(clean)
	cfg.Path = strings.TrimPrefix(filepath.ToSlash(clean), volume+"/")
	return volume + "/", cfg
}

// configureVault registers a vault with rclone under its remote name.
func configureVault(ctx context.Context, client *rcd.Client, v settings.Vault) error {
	base, cfg := placeOf(v)
	return vault.Configure(ctx, client, remoteName(v.ID), base, cfg)
}

// newVaultID derives the ID of a new vault from where it is. The same place
// gets the same ID — and so the same cache folder — if the vault is removed
// and added again, so files that waited in the cache are found again.
func newVaultID(v settings.Vault, taken []settings.Vault) string {
	sum := sha256.Sum256([]byte(placeKey(v) + "\n" + strings.ToLower(location(v))))
	digest := hex.EncodeToString(sum[:])
	for length := 8; ; length += 4 {
		id := "v" + digest[:length]
		if !hasID(taken, id) || length >= len(digest) {
			return id
		}
	}
}

// placeKey tells the places of vaults apart: a local folder, or the account
// that holds the folder. The pCloud account taken over from CryptBridge 0.6
// keeps the key its vaults had then ("pcloud"), so that their IDs stay.
func placeKey(v settings.Vault) string {
	switch {
	case v.Kind == settings.KindLocal:
		return settings.KindLocal
	case v.Account == settings.MigratedAccountID:
		return "pcloud"
	}
	return settings.KindCloud + "\n" + v.Account
}

// samePlace reports whether two vaults are in the same account, or both on
// this PC.
func samePlace(a, b settings.Vault) bool {
	return a.Kind == b.Kind && (a.Kind == settings.KindLocal || a.Account == b.Account)
}

func hasID(vaults []settings.Vault, id string) bool {
	for _, v := range vaults {
		if v.ID == id {
			return true
		}
	}
	return false
}

// checkName cleans a vault name and makes sure it can be shown in Explorer
// and is not used by another vault.
func checkName(name, id string, vaults []settings.Vault) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("give the vault a name")
	case utf8.RuneCountInString(name) > maxNameLength:
		return "", fmt.Errorf("the name may have at most %d characters", maxNameLength)
	case strings.ContainsAny(name, forbiddenNameChars):
		return "", fmt.Errorf("the name cannot contain any of %s", forbiddenNameChars)
	case strings.ContainsFunc(name, func(r rune) bool { return r < ' ' }):
		return "", errors.New("the name cannot contain control characters")
	}
	for _, other := range vaults {
		if other.ID != id && strings.EqualFold(other.Name, name) {
			return "", fmt.Errorf("another vault is already called %q", other.Name)
		}
	}
	return name, nil
}

// checkLetter cleans a drive letter and makes sure no other vault uses it.
func checkLetter(letter, id string, vaults []settings.Vault) (string, error) {
	letter, err := drive.NormaliseLetter(letter)
	if err != nil {
		return "", err
	}
	for _, other := range vaults {
		if other.ID != id && other.Letter == letter {
			return "", fmt.Errorf("drive %s: already belongs to the vault %q", letter, other.Name)
		}
	}
	return letter, nil
}

// cleanLocalFolder checks the folder chosen for a local vault and returns it
// cleaned. It refuses places where a vault would get in the way of Windows,
// of CryptBridge itself, or of another vault's drive.
func cleanLocalFolder(path string, vaults []settings.Vault, paths appdir.Paths, cacheDir string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("choose a folder for the vault")
	}
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' || !filepath.IsAbs(path) {
		return "", errors.New(`choose a folder on a drive, such as E:\Vault. A network folder (\\server\share) must be connected as a drive letter first`)
	}
	path = filepath.Clean(path)
	letter := strings.ToUpper(volume[:1])
	if path == volume+`\` {
		return "", fmt.Errorf("choose a folder on %s, not the whole drive", volume)
	}
	for _, v := range vaults {
		if v.Letter == letter {
			return "", fmt.Errorf("%s is the drive of the vault %q. A vault cannot be kept inside another vault's drive", volume, v.Name)
		}
	}
	for _, protected := range protectedFolders(paths, cacheDir) {
		if overlaps(path, protected, `\`) {
			return "", fmt.Errorf("%s cannot hold a vault: it is or contains %s, which Windows or CryptBridge needs for itself", path, protected)
		}
	}
	return path, nil
}

// protectedFolders are the folders a local vault must neither be in nor
// contain: Windows, the programs, and CryptBridge's own files (its cache
// holds files decrypted).
func protectedFolders(paths appdir.Paths, cacheDir string) []string {
	folders := []string{cacheDir, filepath.Dir(paths.SettingsFile), filepath.Dir(paths.StateFile), paths.LogDir, paths.BinDir}
	for _, name := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		if dir := os.Getenv(name); dir != "" {
			folders = append(folders, dir)
		}
	}
	return folders
}

// overlaps reports whether two folders are the same or one is inside the
// other, ignoring upper and lower case. Windows, pCloud, OneDrive and Dropbox
// ignore it too; Google Drive and MEGA do not, where this is merely on the
// safe side.
func overlaps(a, b, separator string) bool {
	a = strings.TrimSuffix(strings.ToLower(a), separator)
	b = strings.TrimSuffix(strings.ToLower(b), separator)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+separator) || strings.HasPrefix(b, a+separator)
}

// checkNoOverlap refuses a vault in the same folder as another vault, or in
// a folder inside it or around it: two drives on the same files would
// overwrite each other's changes.
func checkNoOverlap(candidate settings.Vault, vaults []settings.Vault) error {
	for _, other := range vaults {
		if other.ID == candidate.ID || !samePlace(candidate, other) || other.Config.Path == "" {
			continue
		}
		separator := "/"
		if candidate.Kind == settings.KindLocal {
			separator = `\`
		}
		if overlaps(location(candidate), location(other), separator) {
			return fmt.Errorf("%s overlaps with the vault %q in %s. Two vaults cannot share a folder", location(candidate), other.Name, location(other))
		}
	}
	return nil
}
