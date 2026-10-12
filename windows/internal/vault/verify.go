// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package vault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

const (
	// CheckFileName is the plain name of the small file every CryptBridge
	// vault carries. With standard name encryption, its encrypted name can
	// only be produced with the right keys, so finding it proves the keys
	// with certainty. With other name encryption, its content proves them.
	CheckFileName = ".cryptbridge-vault"

	checkFileContent = "This file lets CryptBridge verify the keys of this vault.\r\n" +
		"Deleting it does no harm to your files, but please keep it.\r\n"

	// probeRemote is a temporary remote used while trying keys, so that the
	// real vault remote is never touched by a guess.
	probeRemote = "cbprobe"

	// contentProbeRemote is a second temporary remote with the same keys. It
	// leaves names as they are stored and decrypts only the content, so it
	// reads a file whatever the keys make of its name.
	contentProbeRemote = "cbcontent"

	// suffixNone is rclone's word for "no file ending" with plain names.
	suffixNone = "none"

	// maxNamesToCheck limits how many existing names are decrypted when a
	// vault has no check file. A single wrong key passes one name by chance
	// with a probability of about 1 in 256; eight names make that negligible.
	maxNamesToCheck = 8

	// An encrypted file starts with a 32-byte header (marker and nonce), and
	// every block of content carries a 16-byte authenticator. A file of just
	// the header is empty and opens with any keys, so only larger files can
	// prove them.
	cryptHeaderSize    = 32
	cryptBlockOverhead = 16

	// maxContentCheckSize limits which files are read to prove keys, because
	// rclone reads such a file completely. The encrypted check file has
	// 168 bytes.
	maxContentCheckSize = 1 << 20

	// fingerprintProbe is a fixed path whose encrypted form identifies a set
	// of keys. The Linux setup computes the same value the same way.
	fingerprintProbe = "cryptbridge/fingerprint"
)

// Verdict is the outcome of a key check.
type Verdict string

const (
	VerdictEmpty        Verdict = "empty"        // nothing in the folder yet: a new vault
	VerdictMatch        Verdict = "match"        // the keys open the existing vault
	VerdictMismatch     Verdict = "mismatch"     // the keys do NOT fit — writing would split the vault
	VerdictUnverifiable Verdict = "unverifiable" // nothing in the folder can prove the keys
)

// Verification reports what Verify found.
type Verification struct {
	Verdict        Verdict `json:"verdict"`
	HasCheckFile   bool    `json:"hasCheckFile"`
	NamesChecked   int     `json:"namesChecked"`
	NamesOpened    int     `json:"namesOpened"`
	ContentChecked bool    `json:"contentChecked"` // a file was read to test the keys
}

// ErrNoFingerprint is returned for vaults without file name encryption: their
// names do not depend on the keys, so no fingerprint can be derived.
var ErrNoFingerprint = errors.New("a fingerprint needs encrypted file names")

// Verify checks whether the keys in c open the vault folder on baseRemote
// (e.g. "cbpcloud:"). Nothing is written.
func Verify(ctx context.Context, client *rcd.Client, baseRemote string, c Config) (Verification, error) {
	var result Verification
	if err := Configure(ctx, client, probeRemote, baseRemote, c); err != nil {
		return result, err
	}
	defer Remove(context.WithoutCancel(ctx), client, probeRemote)

	items, err := listDir(ctx, client, baseRemote, c.NormalisedPath(), false)
	if isNotFound(err) || (err == nil && len(items) == 0) {
		result.Verdict = VerdictEmpty
		return result, nil
	}
	if err != nil {
		return result, err
	}

	checkName, err := encodeOne(ctx, client, probeRemote+":", CheckFileName)
	if err != nil {
		return result, err
	}
	result.HasCheckFile = slices.ContainsFunc(items, func(item listItem) bool {
		return !item.IsDir && item.Name == checkName
	})

	if c.FilenameEncryption != EncryptionStandard {
		// Obfuscated and plain names come out of any keys, the name of the
		// check file too (measured with obfuscate: about one wrong password
		// in 25 produces it). Only the content proves the keys.
		return verifyByContent(ctx, client, baseRemote, c, items, result)
	}
	if result.HasCheckFile {
		result.Verdict = VerdictMatch
		return result, nil
	}
	result.NamesChecked, result.NamesOpened, err = decodeNames(ctx, client, c, items)
	if err != nil {
		return result, err
	}
	switch {
	case result.NamesChecked == 0:
		result.Verdict = VerdictUnverifiable
	case result.NamesOpened == result.NamesChecked:
		result.Verdict = VerdictMatch
	default:
		result.Verdict = VerdictMismatch
	}
	return result, nil
}

// verifyByContent checks the keys of a vault whose names prove nothing by
// reading one of its files. The names must still follow the vault's naming:
// the content proves password and salt, but not the name settings, and
// wrong name settings would mix two kinds of names in one vault.
func verifyByContent(ctx context.Context, client *rcd.Client, baseRemote string, c Config, items []listItem, result Verification) (Verification, error) {
	var err error
	result.NamesChecked, result.NamesOpened, err = decodeNames(ctx, client, c, items)
	if err != nil {
		return result, err
	}
	if result.NamesOpened < result.NamesChecked {
		result.Verdict = VerdictMismatch
		return result, nil
	}

	sample, found := contentSample(items)
	if !found {
		result.Verdict = VerdictUnverifiable
		return result, nil
	}
	opened, err := opensContent(ctx, client, baseRemote, c, sample.Name)
	if err != nil {
		return result, err
	}
	result.ContentChecked = true
	if opened {
		result.Verdict = VerdictMatch
	} else {
		result.Verdict = VerdictMismatch
	}
	return result, nil
}

// decodeNames decrypts up to maxNamesToCheck of the listed names with the
// keys of the probe remote and counts how many of them opened.
func decodeNames(ctx context.Context, client *rcd.Client, c Config, items []listItem) (checked, opened int, err error) {
	for _, item := range items {
		if checked == maxNamesToCheck {
			break
		}
		// Plain folder names prove nothing.
		if item.IsDir && !encryptsFolderNames(c) {
			continue
		}
		checked++
		var decrypted bool
		decrypted, err = decodeOne(ctx, client, probeRemote+":", item.Name)
		if err != nil {
			return checked, opened, err
		}
		if decrypted {
			opened++
		}
	}
	return checked, opened, nil
}

// encryptsFolderNames reports whether rclone changes folder names: with file
// name encryption off it leaves them plain, whatever the folder setting says.
func encryptsFolderNames(c Config) bool {
	return c.DirectoryNameEncryption && c.FilenameEncryption != EncryptionOff
}

// contentSample picks the smallest file that can prove keys: larger than an
// empty encrypted file, and small enough to be read quickly.
func contentSample(items []listItem) (listItem, bool) {
	var sample listItem
	found := false
	for _, item := range items {
		if item.IsDir || item.Size <= cryptHeaderSize+cryptBlockOverhead || item.Size > maxContentCheckSize {
			continue
		}
		if !found || item.Size < sample.Size {
			sample, found = item, true
		}
	}
	return sample, found
}

// opensContent reports whether the keys of c decrypt the file stored under
// name in the vault folder. Content is authenticated, so wrong keys fail on
// its first block; that is a normal outcome (false, nil). Other failures,
// such as a lost connection, are returned as errors.
func opensContent(ctx context.Context, client *rcd.Client, baseRemote string, c Config, name string) (bool, error) {
	content := c
	content.FilenameEncryption = EncryptionOff
	content.DirectoryNameEncryption = false
	content.Suffix = suffixNone
	// A remote of its own, not a variant of the probe remote such as
	// "cbprobe,suffix=none:": rclone keeps such variants in its cache with
	// the keys they were first used with (measured with rclone 1.75.1).
	if err := Configure(ctx, client, contentProbeRemote, baseRemote, content); err != nil {
		return false, err
	}
	defer Remove(context.WithoutCancel(ctx), client, contentProbeRemote)

	// The hash is not needed: reading the content through the keys is the test.
	request := map[string]any{"fs": contentProbeRemote + ":", "remote": name, "hashType": "md5", "download": true}
	err := client.Call(ctx, "operations/hashsumfile", request, nil)
	if err == nil {
		return true, nil
	}
	var apiErr *rcd.APIError
	if errors.As(err, &apiErr) && strings.Contains(apiErr.Message, "failed to authenticate decrypted block") {
		return false, nil
	}
	return false, fmt.Errorf("read a file to check the keys: %w", err)
}

// Fingerprint returns a short code such as "7F3A-91C2" that is identical on
// every computer using the same vault folder with the same keys and
// settings, and different otherwise. It reveals nothing about the keys.
func Fingerprint(ctx context.Context, client *rcd.Client, baseRemote string, c Config) (string, error) {
	if c.FilenameEncryption == EncryptionOff {
		return "", ErrNoFingerprint
	}
	if err := Configure(ctx, client, probeRemote, baseRemote, c); err != nil {
		return "", err
	}
	defer Remove(context.WithoutCancel(ctx), client, probeRemote)

	encoded, err := encodeOne(ctx, client, probeRemote+":", fingerprintProbe)
	if err != nil {
		return "", err
	}
	return formatFingerprint(c.NormalisedPath(), encoded), nil
}

// formatFingerprint hashes folder and encrypted probe into "XXXX-XXXX".
// The Linux setup does the same: printf '%s\n%s' "$path" "$encoded" | sha256sum.
func formatFingerprint(path, encoded string) string {
	sum := sha256.Sum256([]byte(path + "\n" + encoded))
	code := strings.ToUpper(hex.EncodeToString(sum[:4]))
	return code[:4] + "-" + code[4:]
}

// WriteCheckFile places the check file into the vault behind remote
// (e.g. "cbvault:").
func WriteCheckFile(ctx context.Context, client *rcd.Client, remote string) error {
	dir, err := os.MkdirTemp("", "cryptbridge-check-")
	if err != nil {
		return fmt.Errorf("prepare check file: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, CheckFileName), []byte(checkFileContent), 0o600); err != nil {
		return fmt.Errorf("prepare check file: %w", err)
	}

	request := map[string]any{
		"srcFs":     dir,
		"srcRemote": CheckFileName,
		"dstFs":     remote,
		"dstRemote": CheckFileName,
	}
	if err := client.Call(ctx, "operations/copyfile", request, nil); err != nil {
		return fmt.Errorf("write check file: %w", err)
	}
	return nil
}

// listItem is one entry of an rclone listing.
type listItem struct {
	Path  string `json:"Path"`
	Name  string `json:"Name"`
	Size  int64  `json:"Size"`
	IsDir bool   `json:"IsDir"`
}

// listDir lists a folder of fs; with recurse, everything below it.
func listDir(ctx context.Context, client *rcd.Client, fs, dir string, recurse bool) ([]listItem, error) {
	var answer struct {
		List []listItem `json:"list"`
	}
	request := map[string]any{"fs": fs, "remote": dir, "opt": map[string]any{"recurse": recurse}}
	if err := client.Call(ctx, "operations/list", request, &answer); err != nil {
		return nil, err
	}
	return answer.List, nil
}

// isNotFound recognises rclone's answer for a folder that does not exist.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "directory not found")
}

// encodeOne encrypts a plain path with the keys of remote.
func encodeOne(ctx context.Context, client *rcd.Client, remote, plain string) (string, error) {
	var answer struct {
		Result []string `json:"result"`
	}
	request := map[string]any{"command": "encode", "fs": remote, "arg": []string{plain}}
	if err := client.Call(ctx, "backend/command", request, &answer); err != nil {
		return "", fmt.Errorf("encrypt test name: %w", err)
	}
	if len(answer.Result) != 1 {
		return "", fmt.Errorf("encrypt test name: unexpected answer %v", answer.Result)
	}
	return answer.Result[0], nil
}

// decodeOne reports whether the keys of remote decrypt an encrypted name.
// A name that does not decrypt is a normal outcome (false, nil); only real
// failures such as a lost connection are returned as errors.
func decodeOne(ctx context.Context, client *rcd.Client, remote, encrypted string) (bool, error) {
	request := map[string]any{"command": "decode", "fs": remote, "arg": []string{encrypted}}
	err := client.Call(ctx, "backend/command", request, nil)
	if err == nil {
		return true, nil
	}
	var apiErr *rcd.APIError
	if errors.As(err, &apiErr) && strings.Contains(apiErr.Message, "failed to decrypt") {
		return false, nil
	}
	return false, fmt.Errorf("decrypt test name: %w", err)
}
