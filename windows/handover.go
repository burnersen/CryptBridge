// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/transfer"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// This file holds what the window offers for passing the setup on: the
// encrypted transfer block for the Linux server, the emergency sheet and
// the rclone download.

// TransferResult is shown in the window: the code and the block.
type TransferResult struct {
	Code  string `json:"code"`
	Block string `json:"block"`
}

// CreateTransfer packs sign-in and vault keys into an encrypted block for
// the Linux setup. Each call makes a new code. With the app lock on, the
// app password is asked for again, as the block hands the keys on.
func (a *App) CreateTransfer(appPassword string) (TransferResult, error) {
	if err := a.core.VerifyPassword(appPassword); err != nil {
		return TransferResult{}, err
	}
	snap := a.core.Snapshot()
	if !snap.SignedIn || !snap.HasVault {
		return TransferResult{}, errors.New("sign in to pCloud and choose a vault first")
	}
	code, err := transfer.NewCode()
	if err != nil {
		return TransferResult{}, err
	}
	block, err := transfer.Seal(transfer.Bundle{
		CreatedAt:   time.Now().UTC(),
		PCloud:      a.core.PCloudAccount(),
		Vault:       a.core.VaultConfig(),
		Fingerprint: a.core.Fingerprint(),
	}, code)
	if err != nil {
		return TransferResult{}, err
	}
	return TransferResult{Code: code, Block: block}, nil
}

// SaveTransfer lets the user save the block as a file (e.g. to copy it to
// the server over the remote desktop). Returns the chosen path, "" if the
// user cancelled.
func (a *App) SaveTransfer(block string) (string, error) {
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save the transfer block",
		DefaultFilename: "CryptBridge-transfer.cbtransfer",
		Filters:         []runtime.FileFilter{{DisplayName: "CryptBridge transfer (*.cbtransfer)", Pattern: "*.cbtransfer"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(block), 0o600)
}

// CopyText puts text on the clipboard.
func (a *App) CopyText(text string) error {
	return runtime.ClipboardSetText(a.ctx, text)
}

// SheetData is everything printed on the emergency sheet.
type SheetData struct {
	CreatedAt               string `json:"createdAt"`
	Region                  string `json:"region"`
	PCloudHost              string `json:"pcloudHost"`
	VaultPath               string `json:"vaultPath"`
	Password                string `json:"password"`
	Salt                    string `json:"salt"`
	FilenameEncryption      string `json:"filenameEncryption"`
	DirectoryNameEncryption bool   `json:"directoryNameEncryption"`
	FilenameEncoding        string `json:"filenameEncoding"`
	Suffix                  string `json:"suffix"`
	Fingerprint             string `json:"fingerprint"`
}

// EmergencySheet returns the data for the printable emergency sheet. It is
// only rendered in the window for printing and never written to disk. With
// the app lock on, the app password is asked for again.
func (a *App) EmergencySheet(appPassword string) (SheetData, error) {
	if err := a.core.VerifyPassword(appPassword); err != nil {
		return SheetData{}, err
	}
	snap := a.core.Snapshot()
	if !snap.HasVault {
		return SheetData{}, errors.New("choose a vault first")
	}
	v := a.core.VaultConfig()
	account := a.core.PCloudAccount()
	region := "US"
	if account.Hostname == pcloud.HostEU {
		region = "EU (Europe)"
	}
	return SheetData{
		CreatedAt:               time.Now().Format("2006-01-02 15:04"),
		Region:                  region,
		PCloudHost:              account.Hostname,
		VaultPath:               v.NormalisedPath(),
		Password:                v.Password,
		Salt:                    v.Password2,
		FilenameEncryption:      v.FilenameEncryption,
		DirectoryNameEncryption: v.DirectoryNameEncryption,
		FilenameEncoding:        v.FilenameEncoding,
		Suffix:                  v.Suffix,
		Fingerprint:             a.core.Fingerprint(),
	}, nil
}

// DownloadRclone fetches rclone from rclone.org (checksum verified) and
// reports progress to the window ("download-progress", 0..100).
func (a *App) DownloadRclone() error {
	return a.core.InstallRclone(context.WithoutCancel(a.coreCtx), func(done, total int64) {
		if total > 0 {
			runtime.EventsEmit(a.ctx, "download-progress", int(done*100/total))
		}
	})
}
