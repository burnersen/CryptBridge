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
	"strings"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

const (
	// CheckFileName is the plain name of the small file every CryptBridge
	// vault carries. Its encrypted name can only be produced with the right
	// keys, so finding it proves the keys with certainty.
	CheckFileName = ".cryptbridge-vault"

	checkFileContent = "This file lets CryptBridge verify the keys of this vault.\r\n" +
		"Deleting it does no harm to your files, but please keep it.\r\n"

	// probeRemote is a temporary remote used while trying keys, so that the
	// real vault remote is never touched by a guess.
	probeRemote = "cbprobe"

	// maxNamesToCheck limits how many existing names are decrypted when a
	// vault has no check file. A single wrong key passes one name by chance
	// with a probability of about 1 in 256; eight names make that negligible.
	maxNamesToCheck = 8

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
	VerdictUnverifiable Verdict = "unverifiable" // names are not encrypted, so they prove nothing
)

// Verification reports what Verify found.
type Verification struct {
	Verdict      Verdict `json:"verdict"`
	HasCheckFile bool    `json:"hasCheckFile"`
	NamesChecked int     `json:"namesChecked"`
	NamesOpened  int     `json:"namesOpened"`
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
	for _, item := range items {
		if !item.IsDir && item.Name == checkName {
			result.Verdict = VerdictMatch
			result.HasCheckFile = true
			return result, nil
		}
	}

	if c.FilenameEncryption == EncryptionOff {
		result.Verdict = VerdictUnverifiable
		return result, nil
	}

	for _, item := range items {
		if result.NamesChecked == maxNamesToCheck {
			break
		}
		// Plain folder names prove nothing when folder names are not encrypted.
		if item.IsDir && !c.DirectoryNameEncryption {
			continue
		}
		result.NamesChecked++
		opened, err := decodeOne(ctx, client, probeRemote+":", item.Name)
		if err != nil {
			return result, err
		}
		if opened {
			result.NamesOpened++
		}
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
