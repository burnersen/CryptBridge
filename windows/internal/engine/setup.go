// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"regexp"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/prereq"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// errConnected is returned by actions that must not run while the drive is
// connected, because they would change what the drive shows.
var errConnected = errors.New("disconnect the drive first")

// passwordAlphabet leaves out characters that are easily confused when a
// password is copied by hand from the emergency sheet (0/O, 1/l/I).
const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// passwordLength gives about 186 bits of randomness with this alphabet.
const passwordLength = 32

// NewPassword returns a strong random password for a new vault.
func NewPassword() (string, error) {
	out := make([]byte, passwordLength)
	limit := big.NewInt(int64(len(passwordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("create random password: %w", err)
		}
		out[i] = passwordAlphabet[n.Int64()]
	}
	return string(out), nil
}

// InstallRclone downloads rclone (checksum verified) into CryptBridge's own
// folder and starts working with it.
func (e *Engine) InstallRclone(ctx context.Context, progress prereq.Progress) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	rclone, err := prereq.DownloadRclone(ctx, e.paths.BinDir, progress)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.rclone = &rclone
	e.mu.Unlock()
	if err := e.prepareLocked(ctx); err != nil {
		e.setPhase(PhaseError, err.Error())
		return err
	}
	e.settlePhaseLocked(ctx, true)
	return nil
}

// Fingerprint returns the check code of the stored vault ("" if none).
func (e *Engine) Fingerprint() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fingerprint
}

// SignIn opens the pCloud sign-in in the browser, finds the account's
// region and stores the sign-in.
func (e *Engine) SignIn(ctx context.Context, onLink func(string)) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	client, err := e.idleClientLocked(ctx)
	if err != nil {
		return err
	}

	e.mu.Lock()
	exe := e.rclone.Path
	e.mu.Unlock()
	token, err := pcloud.Authorize(ctx, exe, onLink)
	if err != nil {
		return err
	}
	host, err := pcloud.FindRegion(ctx, client, token)
	if err != nil {
		return err
	}
	account := pcloud.Account{Hostname: host, Token: token}
	if err := pcloud.Configure(ctx, client, pcloudRemote, account); err != nil {
		return err
	}

	e.mu.Lock()
	e.settings.PCloud = account
	s := e.settings
	e.mu.Unlock()
	e.lastUsage = time.Time{} // ask pCloud for the free space right away
	if err := settings.Save(e.paths.SettingsFile, s); err != nil {
		return err
	}
	// The vault sits on top of the account: register it again so that it
	// uses the new sign-in.
	if s.HasVault() {
		if err := configureVault(ctx, client, s); err != nil {
			return err
		}
	}
	e.settlePhaseLocked(ctx, false)
	return nil
}

// SignOut forgets the pCloud sign-in. The vault keys are kept.
func (e *Engine) SignOut(ctx context.Context) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	client, err := e.idleClientLocked(ctx)
	if err != nil {
		return err
	}
	_ = vault.Remove(ctx, client, vaultRemote)
	_ = vault.Remove(ctx, client, pcloudRemote)

	e.mu.Lock()
	e.settings.PCloud = pcloud.Account{}
	e.usage = nil
	s := e.settings
	e.mu.Unlock()
	if err := settings.Save(e.paths.SettingsFile, s); err != nil {
		return err
	}
	e.settlePhaseLocked(ctx, false)
	return nil
}

// FindVaults scans pCloud for existing vaults.
func (e *Engine) FindVaults(ctx context.Context) ([]vault.Candidate, error) {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	client, err := e.signedInClientLocked(ctx)
	if err != nil {
		return nil, err
	}
	return vault.Detect(ctx, client, pcloudRemote+":")
}

// VaultCheck is the result of testing keys against a folder.
type VaultCheck struct {
	Verification vault.Verification `json:"verification"`
	Fingerprint  string             `json:"fingerprint"`
}

// CheckVault tests keys against a folder without saving anything.
func (e *Engine) CheckVault(ctx context.Context, cfg vault.Config) (VaultCheck, error) {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	client, err := e.signedInClientLocked(ctx)
	if err != nil {
		return VaultCheck{}, err
	}
	return checkVault(ctx, client, cfg)
}

func checkVault(ctx context.Context, client *rcd.Client, cfg vault.Config) (VaultCheck, error) {
	var check VaultCheck
	verification, err := vault.Verify(ctx, client, pcloudRemote+":", cfg)
	if err != nil {
		return check, err
	}
	check.Verification = verification
	fingerprint, err := vault.Fingerprint(ctx, client, pcloudRemote+":", cfg)
	if err != nil && !errors.Is(err, vault.ErrNoFingerprint) {
		return check, err
	}
	check.Fingerprint = fingerprint
	return check, nil
}

// UseVault stores a vault after checking it. With create, the folder must
// be empty (a new vault). Without create, the keys must open the existing
// vault; a vault whose names are not encrypted cannot be checked and is
// only accepted with acceptUnverifiable.
func (e *Engine) UseVault(ctx context.Context, cfg vault.Config, create, acceptUnverifiable bool) (VaultCheck, error) {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	client, err := e.signedInClientLocked(ctx)
	if err != nil {
		return VaultCheck{}, err
	}
	check, err := checkVault(ctx, client, cfg)
	if err != nil {
		return check, err
	}

	verdict := check.Verification.Verdict
	switch {
	case create && verdict != vault.VerdictEmpty:
		return check, errors.New("this folder is not empty. Choose an empty or new folder for a new vault, or open the existing vault instead")
	case !create && verdict == vault.VerdictEmpty:
		return check, errors.New("this folder is empty, so there is no vault to open. Create a new vault instead")
	case verdict == vault.VerdictMismatch:
		return check, errors.New("these keys do NOT open the vault in this folder. Check password, salt and the advanced settings — nothing was changed")
	case verdict == vault.VerdictUnverifiable && !acceptUnverifiable:
		return check, errors.New("this vault does not encrypt file names, so the keys cannot be checked")
	}

	cfg.Path = cfg.NormalisedPath()
	if err := vault.Configure(ctx, client, vaultRemote, pcloudRemote+":", cfg); err != nil {
		return check, err
	}
	if verdict != vault.VerdictUnverifiable && !check.Verification.HasCheckFile {
		if err := vault.WriteCheckFile(ctx, client, vaultRemote+":"); err != nil {
			return check, err
		}
	}

	e.mu.Lock()
	e.settings.Vault = cfg
	e.fingerprint = check.Fingerprint
	s := e.settings
	e.mu.Unlock()
	if err := settings.Save(e.paths.SettingsFile, s); err != nil {
		return check, err
	}
	e.settlePhaseLocked(ctx, false)
	return check, nil
}

// VaultConfig returns the stored vault including its keys; used for the
// emergency sheet and the transfer to the server.
func (e *Engine) VaultConfig() vault.Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings.Vault
}

// PCloudAccount returns the stored sign-in; used for the transfer to the server.
func (e *Engine) PCloudAccount() pcloud.Account {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings.PCloud
}

// Options are the settings the user can change in the window.
type Options struct {
	Letter            string `json:"letter"`
	CacheDir          string `json:"cacheDir"`
	CacheMaxSize      string `json:"cacheMaxSize"`
	CacheMinFreeSpace string `json:"cacheMinFreeSpace"`
	AutoConnect       bool   `json:"autoConnect"`
	AutoReconnect     bool   `json:"autoReconnect"`
}

// sizePattern accepts sizes like "20G", "512M" or "100".
var sizePattern = regexp.MustCompile(`^[1-9][0-9]*[KMGT]?$`)

// UpdateOptions validates and stores the options. Drive letter and cache
// folder can only change while the drive is disconnected.
func (e *Engine) UpdateOptions(ctx context.Context, o Options) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()

	letter, err := drive.NormaliseLetter(o.Letter)
	if err != nil {
		return err
	}
	if !sizePattern.MatchString(o.CacheMaxSize) || !sizePattern.MatchString(o.CacheMinFreeSpace) {
		return errors.New(`cache sizes must look like "20G" or "500M"`)
	}
	if !filepath.IsAbs(o.CacheDir) {
		return errors.New("the cache folder must be a full path such as D:\\CryptBridgeCache")
	}

	e.mu.Lock()
	old := e.settings
	connected := e.wantConnected
	e.mu.Unlock()

	driveChanged := letter != old.Drive.Letter || o.CacheDir != old.Drive.CacheDir ||
		o.CacheMaxSize != old.Drive.CacheMaxSize || o.CacheMinFreeSpace != old.Drive.CacheMinFreeSpace
	if driveChanged && connected {
		return errConnected
	}

	updated := old
	updated.Drive = settings.Drive{Letter: letter, CacheDir: o.CacheDir, CacheMaxSize: o.CacheMaxSize, CacheMinFreeSpace: o.CacheMinFreeSpace}
	updated.AutoConnect = o.AutoConnect
	updated.AutoReconnect = o.AutoReconnect
	if err := settings.Save(e.paths.SettingsFile, updated); err != nil {
		return err
	}

	e.mu.Lock()
	e.settings = updated
	client := e.client
	e.mu.Unlock()

	// rclone fixes its cache folder when it starts: stop it, the next
	// connect starts a fresh one with the new folder.
	if o.CacheDir != old.Drive.CacheDir && client != nil {
		e.mu.Lock()
		e.client = nil
		e.mu.Unlock()
		if err := rcd.Stop(ctx, client, e.paths.StateFile); err != nil {
			return err
		}
	}
	e.publish()
	return nil
}

// idleClientLocked returns a running rclone while the drive is not connected.
func (e *Engine) idleClientLocked(ctx context.Context) (*rcd.Client, error) {
	e.mu.Lock()
	connected := e.wantConnected
	e.mu.Unlock()
	if connected {
		return nil, errConnected
	}
	if err := e.ensureRcloneFoundLocked(); err != nil {
		return nil, err
	}
	return e.ensureDaemonLocked(ctx)
}

// signedInClientLocked returns a running rclone that knows the account.
func (e *Engine) signedInClientLocked(ctx context.Context) (*rcd.Client, error) {
	e.mu.Lock()
	signedIn := e.settings.SignedIn()
	e.mu.Unlock()
	if !signedIn {
		return nil, errors.New("sign in to pCloud first")
	}
	if err := e.prepareLocked(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client, nil
}

// configureVault registers the stored vault with rclone.
func configureVault(ctx context.Context, client *rcd.Client, s settings.Settings) error {
	return vault.Configure(ctx, client, vaultRemote, pcloudRemote+":", s.Vault)
}

// refreshFingerprint computes the fingerprint of the stored vault.
func (e *Engine) refreshFingerprint(ctx context.Context, client *rcd.Client, s settings.Settings) {
	fingerprint, err := vault.Fingerprint(ctx, client, pcloudRemote+":", s.Vault)
	if err != nil {
		return
	}
	e.mu.Lock()
	e.fingerprint = fingerprint
	e.mu.Unlock()
}
