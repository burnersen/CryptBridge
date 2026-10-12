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
	"slices"
	"strings"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/drive"
	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/prereq"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
	"github.com/burnersen/CryptBridge/windows/internal/vault"
)

// errConnected is returned by actions that must not run while a drive is
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
	if e.isLocked() {
		return errLocked
	}
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
	e.settleLocked(ctx, true)
	return nil
}

// Fingerprint returns the check code of a vault ("" if unknown).
func (e *Engine) Fingerprint(id string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runtimeLocked(id).fingerprint
}

// SignIn opens the pCloud sign-in in the browser (through the user's own
// pCloud app, if one is set), finds the account's region and stores the
// sign-in. The pCloud drives must be disconnected; local drives may stay.
func (e *Engine) SignIn(ctx context.Context, onLink func(string)) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	client, err := e.pcloudIdleClientLocked(ctx)
	if err != nil {
		return err
	}

	e.mu.Lock()
	exe := e.rclone.Path
	app := e.settings.PCloudApp
	e.mu.Unlock()
	token, err := pcloud.Authorize(ctx, exe, app, onLink)
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

	updated := e.currentSettings()
	updated.PCloud = account
	updated.SignInAppID = app.ClientID
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	e.signInProblem = "" // FindRegion has just seen pCloud accept it
	e.mu.Unlock()
	e.lastUsage = time.Time{} // ask pCloud for the free space right away

	// The pCloud vaults sit on top of the account: register them again so
	// that they use the new sign-in.
	for _, v := range updated.Vaults {
		if v.Kind == settings.KindPCloud && v.HasKeys() {
			if err := configureVault(ctx, client, v); err != nil {
				return fmt.Errorf("vault %q: %w", v.Name, err)
			}
			e.refreshFingerprint(ctx, client, updated, v)
		}
	}
	e.settleLocked(ctx, false)
	return nil
}

// SignOut forgets the pCloud sign-in. The vault keys are kept.
func (e *Engine) SignOut(ctx context.Context) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	client, err := e.pcloudIdleClientLocked(ctx)
	if err != nil {
		return err
	}
	updated := e.currentSettings()
	for _, v := range updated.Vaults {
		if v.Kind == settings.KindPCloud {
			_ = vault.Remove(ctx, client, remoteName(v.ID))
		}
	}
	_ = vault.Remove(ctx, client, pcloudRemote)

	updated.PCloud = pcloud.Account{}
	updated.SignInAppID = ""
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	e.usage = nil
	e.signInProblem = ""
	e.mu.Unlock()
	e.settleLocked(ctx, false)
	return nil
}

// SetPCloudApp stores the codes of the user's own pCloud app; empty codes
// mean rclone's built-in app again. It takes effect with the next sign-in;
// the current sign-in keeps working until then, so the drives may stay
// connected.
func (e *Engine) SetPCloudApp(clientID, clientSecret string) error {
	app, err := pcloud.NewApp(clientID, clientSecret)
	if err != nil {
		return err
	}
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	updated := e.currentSettings()
	updated.PCloudApp = app
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	e.mu.Unlock()
	e.publish()
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
	candidates, err := vault.Detect(ctx, client, pcloudRemote+":")
	return candidates, e.explain(err)
}

// InspectFolder looks into a folder while the user chooses where a vault
// is: a folder in pCloud (kind pcloud, "" is the top level) or a folder on
// this PC. Only names are read.
func (e *Engine) InspectFolder(ctx context.Context, kind, path string) (vault.Folder, error) {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	var place settings.Vault
	switch kind {
	case settings.KindPCloud:
		place = settings.Vault{Kind: kind, Config: vault.Config{Path: path}}
	case settings.KindLocal:
		clean, err := e.cleanLocalFolderLocked(path)
		if err != nil {
			return vault.Folder{}, err
		}
		place = settings.Vault{Kind: kind, Config: vault.Config{Path: clean}}
	default:
		return vault.Folder{}, fmt.Errorf("unknown kind of vault %q", kind)
	}
	client, err := e.clientForKindLocked(ctx, kind)
	if err != nil {
		return vault.Folder{}, err
	}
	base, cfg := placeOf(place)
	folder, err := vault.Inspect(ctx, client, base, cfg.NormalisedPath())
	folder.Path = path
	return folder, e.explain(err)
}

// VaultCheck is the result of testing keys against a folder.
type VaultCheck struct {
	Verification vault.Verification `json:"verification"`
	Fingerprint  string             `json:"fingerprint"`
	VaultID      string             `json:"vaultId"` // set when a vault was added
}

// NewVault is what the window sends to check or add a vault.
type NewVault struct {
	Kind               string       `json:"kind"`
	Name               string       `json:"name"`
	Letter             string       `json:"letter"`
	AutoConnect        bool         `json:"autoConnect"`
	Config             vault.Config `json:"config"`
	Create             bool         `json:"create"`             // a new vault in an empty folder
	AcceptUnverifiable bool         `json:"acceptUnverifiable"` // keys of a vault without encrypted names cannot be checked
}

// CheckVault tests keys against a folder without saving anything.
func (e *Engine) CheckVault(ctx context.Context, req NewVault) (VaultCheck, error) {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	candidate, err := e.placeNewVaultLocked(req)
	if err != nil {
		return VaultCheck{}, err
	}
	client, err := e.clientForKindLocked(ctx, candidate.Kind)
	if err != nil {
		return VaultCheck{}, err
	}
	check, err := checkVault(ctx, client, candidate)
	return check, e.explain(err)
}

// AddVault adds a vault after checking it. With Create, the folder must be
// empty (a new vault). Without, the keys must open the existing vault; a
// vault whose names are not encrypted cannot be checked and is only
// accepted with AcceptUnverifiable. Nothing is written into a folder that
// fails these checks.
func (e *Engine) AddVault(ctx context.Context, req NewVault) (VaultCheck, error) {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	candidate, err := e.placeNewVaultLocked(req)
	if err != nil {
		return VaultCheck{}, err
	}
	s := e.currentSettings()
	if candidate.Name, err = checkName(req.Name, "", s.Vaults); err != nil {
		return VaultCheck{}, err
	}
	if candidate.Letter, err = checkLetter(req.Letter, "", s.Vaults); err != nil {
		return VaultCheck{}, err
	}
	if inUse, err := drive.LetterInUse(candidate.Letter); err != nil {
		return VaultCheck{}, err
	} else if inUse {
		return VaultCheck{}, fmt.Errorf("drive %s: is in use by Windows; choose another letter", candidate.Letter)
	}
	candidate.AutoConnect = req.AutoConnect

	client, err := e.clientForKindLocked(ctx, candidate.Kind)
	if err != nil {
		return VaultCheck{}, err
	}
	check, err := checkVault(ctx, client, candidate)
	if err != nil {
		return check, e.explain(err)
	}
	if err := acceptVerdict(check.Verification.Verdict, req.Create, req.AcceptUnverifiable); err != nil {
		return check, err
	}

	candidate.ID = newVaultID(candidate, s.Vaults)
	if err := configureVault(ctx, client, candidate); err != nil {
		return check, err
	}
	if check.Verification.Verdict != vault.VerdictUnverifiable && !check.Verification.HasCheckFile {
		if err := vault.WriteCheckFile(ctx, client, remoteOf(candidate)); err != nil {
			_ = vault.Remove(ctx, client, remoteName(candidate.ID))
			return check, e.explain(err)
		}
	}
	if candidate.Kind == settings.KindLocal {
		// From now on the drive connects only to this very folder.
		if folder, err := drive.IdentifyFolder(localPath(candidate)); err == nil {
			candidate.Folder = &settings.FolderMark{Volume: folder.Volume, Index: folder.Index}
		}
	}

	s.Vaults = append(s.Vaults, candidate)
	if err := e.saveSettings(s); err != nil {
		_ = vault.Remove(ctx, client, remoteName(candidate.ID))
		return check, err
	}
	e.mu.Lock()
	e.settings = s
	rt := e.runtimeLocked(candidate.ID)
	*rt = vaultRuntime{state: StateDisconnected, fingerprint: check.Fingerprint}
	e.mu.Unlock()
	e.settleLocked(ctx, false)
	check.VaultID = candidate.ID
	return check, nil
}

// acceptVerdict decides whether a checked folder may become a vault.
func acceptVerdict(verdict vault.Verdict, create, acceptUnverifiable bool) error {
	switch {
	case create && verdict != vault.VerdictEmpty:
		return errors.New("this folder is not empty. Choose an empty or new folder for a new vault, or open the existing vault instead")
	case !create && verdict == vault.VerdictEmpty:
		return errors.New("this folder is empty, so there is no vault to open. Create a new vault instead")
	case verdict == vault.VerdictMismatch:
		return errors.New("these keys do NOT open the vault in this folder. Check password, salt and the advanced settings — nothing was changed")
	case verdict == vault.VerdictUnverifiable && !acceptUnverifiable:
		return errors.New("the keys cannot be checked: the file names of this vault prove nothing, and it holds no small file to test the keys with")
	}
	return nil
}

// placeNewVaultLocked checks where a vault to be added is and returns it
// without name, letter and ID: the kind is known, the folder is cleaned and
// may hold a vault, and it does not overlap with a vault already added.
func (e *Engine) placeNewVaultLocked(req NewVault) (settings.Vault, error) {
	if e.isLocked() {
		return settings.Vault{}, errLocked
	}
	candidate := settings.Vault{Kind: req.Kind, Config: req.Config}
	switch req.Kind {
	case settings.KindPCloud:
		candidate.Config.Path = candidate.Config.NormalisedPath()
	case settings.KindLocal:
		clean, err := e.cleanLocalFolderLocked(req.Config.Path)
		if err != nil {
			return settings.Vault{}, err
		}
		candidate.Config.Path = clean
		// Windows does not tell upper from lower case in file names, but
		// base64 names do: two different names could land on one file.
		if candidate.Config.FilenameEncoding == vault.EncodingBase64 {
			return settings.Vault{}, errors.New("vaults with base64 file names cannot be kept on a Windows drive: " +
				"Windows does not tell upper from lower case, so two encrypted names could overwrite each other")
		}
	default:
		return settings.Vault{}, fmt.Errorf("unknown kind of vault %q", req.Kind)
	}
	if err := candidate.Config.Validate(); err != nil {
		return settings.Vault{}, err
	}
	if err := checkNoOverlap(candidate, e.currentSettings().Vaults); err != nil {
		return settings.Vault{}, err
	}
	return candidate, nil
}

// cleanLocalFolderLocked is cleanLocalFolder with the engine's settings.
func (e *Engine) cleanLocalFolderLocked(path string) (string, error) {
	s := e.currentSettings()
	return cleanLocalFolder(path, s.Vaults, e.paths, s.Cache.Dir)
}

// checkVault tests the keys of a vault against its folder, after making
// sure the folder is not inside another vault.
func checkVault(ctx context.Context, client *rcd.Client, v settings.Vault) (VaultCheck, error) {
	var check VaultCheck
	base, cfg := placeOf(v)
	enclosing, inside, err := vault.EnclosingVault(ctx, client, base, cfg.NormalisedPath())
	if err != nil {
		return check, err
	}
	if inside {
		if v.Kind == settings.KindLocal {
			enclosing = filepath.Join(strings.TrimSuffix(base, "/")+`\`, filepath.FromSlash(enclosing))
		}
		return check, fmt.Errorf("this folder is inside the vault in %s. Choose a folder outside of it — or open that vault itself", enclosing)
	}
	verification, err := vault.Verify(ctx, client, base, cfg)
	if err != nil {
		return check, err
	}
	check.Verification = verification
	fingerprint, err := vault.Fingerprint(ctx, client, base, cfg)
	if err != nil && !errors.Is(err, vault.ErrNoFingerprint) {
		return check, err
	}
	check.Fingerprint = fingerprint
	return check, nil
}

// VaultChange is what the window may change on a vault.
type VaultChange struct {
	Name        string `json:"name"`
	Letter      string `json:"letter"`
	AutoConnect bool   `json:"autoConnect"`
}

// UpdateVault changes name, drive letter and "connect on start" of a vault.
// The letter can only change while the drive is disconnected; a new name
// shows in Explorer from the next connect.
func (e *Engine) UpdateVault(id string, change VaultChange) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	s := e.currentSettings()
	index := slices.IndexFunc(s.Vaults, func(v settings.Vault) bool { return v.ID == id })
	if index < 0 {
		return errUnknownVault
	}
	name, err := checkName(change.Name, id, s.Vaults)
	if err != nil {
		return err
	}
	letter, err := checkLetter(change.Letter, id, s.Vaults)
	if err != nil {
		return err
	}
	v := &s.Vaults[index]
	if letter != v.Letter {
		e.mu.Lock()
		rt := e.runtimeLocked(id)
		busy := rt.want || rt.status.Mounted
		e.mu.Unlock()
		if busy {
			return errConnected
		}
		if inUse, err := drive.LetterInUse(letter); err != nil {
			return err
		} else if inUse {
			return fmt.Errorf("drive %s: is in use by Windows; choose another letter", letter)
		}
	}
	v.Name, v.Letter, v.AutoConnect = name, letter, change.AutoConnect
	if err := e.saveSettings(s); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = s
	e.mu.Unlock()
	e.publish()
	return nil
}

// RemoveVault makes CryptBridge forget a vault: its keys and its drive.
// The files of the vault are never touched. The drive must be disconnected,
// and files still waiting in the cache must be written first. confirmName
// must repeat the vault's name, so that no vault is removed by a slip.
func (e *Engine) RemoveVault(ctx context.Context, id, confirmName string) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	s := e.currentSettings()
	v, ok := s.Vault(id)
	if !ok {
		return errUnknownVault
	}
	if !strings.EqualFold(strings.TrimSpace(confirmName), v.Name) {
		return fmt.Errorf("type the name of the vault (%s) to confirm", v.Name)
	}
	e.mu.Lock()
	rt := e.runtimeLocked(id)
	busy := rt.want || rt.status.Mounted
	counting := rt.counting != nil
	client := e.client
	e.mu.Unlock()
	if busy {
		return errConnected
	}
	if counting {
		return errors.New("the size of this vault is being counted; cancel that first")
	}
	waiting, err := drive.WaitingUploads(s.Cache.Dir, remoteName(id))
	if err != nil {
		return err
	}
	if waiting > 0 {
		return fmt.Errorf("%d file(s) wait on this PC to be written into this vault. Connect it and let them finish first — "+
			"removing it now would lose them", waiting)
	}

	if client != nil {
		_ = vault.Remove(ctx, client, remoteName(id))
	}
	s.Vaults = slices.DeleteFunc(s.Vaults, func(other settings.Vault) bool { return other.ID == id })
	if err := e.saveSettings(s); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = s
	delete(e.vaults, id)
	e.unlockConnects = slices.DeleteFunc(e.unlockConnects, func(other string) bool { return other == id })
	e.mu.Unlock()

	// The cache holds recently used files of the vault decrypted. rclone may
	// still hold some of them open; whatever stays is deleted with the next
	// app lock or by hand.
	_ = drive.ClearCache(s.Cache.Dir, remoteName(id))
	e.settleLocked(ctx, false)
	return nil
}

// VaultSecrets returns a vault including its keys; used for the emergency
// sheet and the transfer to the server.
func (e *Engine) VaultSecrets(id string) (settings.Vault, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.locked {
		return settings.Vault{}, errLocked
	}
	v, ok := e.settings.Vault(id)
	if !ok {
		return settings.Vault{}, errUnknownVault
	}
	return v, nil
}

// PCloudAccount returns the stored sign-in; used for the transfer to the server.
func (e *Engine) PCloudAccount() pcloud.Account {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings.PCloud
}

// Options are the settings the user can change on the Settings page.
type Options struct {
	CacheDir          string `json:"cacheDir"`
	CacheMaxSize      string `json:"cacheMaxSize"`
	CacheMinFreeSpace string `json:"cacheMinFreeSpace"`
	AutoReconnect     bool   `json:"autoReconnect"`
	Theme             string `json:"theme"`
}

// sizePattern accepts sizes like "20G", "512M" or "100".
var sizePattern = regexp.MustCompile(`^[1-9][0-9]*[KMGT]?$`)

// UpdateOptions validates and stores the options. The cache can only change
// while every drive is disconnected.
func (e *Engine) UpdateOptions(ctx context.Context, o Options) error {
	e.actionMu.Lock()
	defer e.actionMu.Unlock()
	if e.isLocked() {
		return errLocked
	}
	if !sizePattern.MatchString(o.CacheMaxSize) || !sizePattern.MatchString(o.CacheMinFreeSpace) {
		return errors.New(`cache sizes must look like "20G" or "500M"`)
	}
	if !filepath.IsAbs(o.CacheDir) {
		return errors.New("the cache folder must be a full path such as D:\\CryptBridgeCache")
	}
	switch o.Theme {
	case settings.ThemeAuto, settings.ThemeLight, settings.ThemeDark:
	default:
		return fmt.Errorf("unknown theme %q", o.Theme)
	}

	old := e.currentSettings()
	cache := settings.Cache{Dir: filepath.Clean(o.CacheDir), MaxSize: o.CacheMaxSize, MinFreeSpace: o.CacheMinFreeSpace}
	if cache != old.Cache && e.anyDriveBusy() {
		return errors.New("disconnect all drives before you change the cache")
	}
	for _, v := range old.Vaults {
		if v.Kind == settings.KindLocal && overlaps(localPath(v), cache.Dir, `\`) {
			return fmt.Errorf("the cache cannot be inside or around the vault %q: it holds files decrypted", v.Name)
		}
	}

	updated := old.Clone()
	updated.Cache = cache
	updated.AutoReconnect = o.AutoReconnect
	updated.Theme = o.Theme
	if err := e.saveSettings(updated); err != nil {
		return err
	}
	e.mu.Lock()
	e.settings = updated
	client := e.client
	e.mu.Unlock()

	// rclone fixes its cache folder when it starts: stop it, the next
	// connect starts a fresh one with the new folder.
	if cache.Dir != old.Cache.Dir && client != nil {
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

// anyDriveBusy reports whether a drive is connected or wanted.
func (e *Engine) anyDriveBusy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, rt := range e.vaults {
		if rt.want || rt.status.Mounted {
			return true
		}
	}
	return false
}

// pcloudIdleClientLocked returns a running rclone while no pCloud drive is
// connected: the sign-in under them must not change while they are in use.
func (e *Engine) pcloudIdleClientLocked(ctx context.Context) (*rcd.Client, error) {
	if e.isLocked() {
		return nil, errLocked
	}
	for _, v := range e.currentSettings().Vaults {
		if v.Kind != settings.KindPCloud {
			continue
		}
		e.mu.Lock()
		rt := e.runtimeLocked(v.ID)
		busy := rt.want || rt.status.Mounted
		e.mu.Unlock()
		if busy {
			return nil, fmt.Errorf("disconnect the pCloud drive %s first", describe(v))
		}
	}
	if err := e.ensureRcloneFoundLocked(); err != nil {
		return nil, err
	}
	return e.ensureDaemonLocked(ctx)
}

// clientForKindLocked returns a running rclone for working with vaults of a
// kind: pCloud vaults need the sign-in, local vaults do not.
func (e *Engine) clientForKindLocked(ctx context.Context, kind string) (*rcd.Client, error) {
	if kind == settings.KindPCloud {
		return e.signedInClientLocked(ctx)
	}
	if err := e.prepareLocked(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client, nil
}

// signedInClientLocked returns a running rclone that knows the account.
func (e *Engine) signedInClientLocked(ctx context.Context) (*rcd.Client, error) {
	e.mu.Lock()
	signedIn := e.settings.SignedIn()
	locked := e.locked
	e.mu.Unlock()
	if locked {
		return nil, errLocked
	}
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

// refreshFingerprint computes the check code of a vault. A pCloud vault
// needs the sign-in for it.
func (e *Engine) refreshFingerprint(ctx context.Context, client *rcd.Client, s settings.Settings, v settings.Vault) {
	if client == nil || (v.Kind == settings.KindPCloud && !s.SignedIn()) {
		return
	}
	base, cfg := placeOf(v)
	fingerprint, err := vault.Fingerprint(ctx, client, base, cfg)
	if err != nil {
		return
	}
	e.update(v.ID, func(rt *vaultRuntime) { rt.fingerprint = fingerprint })
}
