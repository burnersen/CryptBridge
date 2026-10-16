// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/settings"
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

// CreateTransfer packs the sign-in to the cloud account and the keys of a
// cloud vault into an encrypted block for the Linux setup. Each call makes a
// new code. With the app lock on, the app password is asked for again, as
// the block hands the keys on.
func (a *App) CreateTransfer(id, appPassword string) (TransferResult, error) {
	if err := a.core.VerifyPassword(appPassword); err != nil {
		return TransferResult{}, err
	}
	v, err := a.core.VaultSecrets(id)
	if err != nil {
		return TransferResult{}, err
	}
	if v.Kind != settings.KindCloud {
		return TransferResult{}, errors.New("only a vault in the cloud can be opened on the Linux server")
	}
	account, err := a.core.AccountSecrets(v.Account)
	if err != nil {
		return TransferResult{}, err
	}
	if !account.SignedIn() {
		return TransferResult{}, fmt.Errorf("sign in to %s first", account.Name)
	}
	code, err := transfer.NewCode()
	if err != nil {
		return TransferResult{}, err
	}
	block, err := transfer.Seal(transfer.Bundle{
		CreatedAt:   time.Now().UTC(),
		Cloud:       transfer.NewCloud(account.Service, account.Remote),
		Vault:       v.Config,
		Fingerprint: a.core.Fingerprint(id),
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
	Name                    string `json:"name"`
	Kind                    string `json:"kind"`         // settings.KindCloud or settings.KindLocal
	Service                 string `json:"service"`      // cloud: rclone's backend name, e.g. "drive"
	ServiceTitle            string `json:"serviceTitle"` // cloud: e.g. "Google Drive"
	AccountName             string `json:"accountName"`  // cloud: the account's name in CryptBridge
	Region                  string `json:"region"`       // pCloud only
	PCloudHost              string `json:"pcloudHost"`   // pCloud only
	VaultPath               string `json:"vaultPath"`    // folder in the cloud or on this PC
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
func (a *App) EmergencySheet(id, appPassword string) (SheetData, error) {
	if err := a.core.VerifyPassword(appPassword); err != nil {
		return SheetData{}, err
	}
	v, err := a.core.VaultSecrets(id)
	if err != nil {
		return SheetData{}, err
	}
	sheet := SheetData{
		CreatedAt:               time.Now().Format("2006-01-02 15:04"),
		Name:                    v.Name,
		Kind:                    v.Kind,
		VaultPath:               v.Config.NormalisedPath(),
		Password:                v.Config.Password,
		Salt:                    v.Config.Password2,
		FilenameEncryption:      v.Config.FilenameEncryption,
		DirectoryNameEncryption: v.Config.DirectoryNameEncryption,
		FilenameEncoding:        v.Config.FilenameEncoding,
		Suffix:                  v.Config.Suffix,
		Fingerprint:             a.core.Fingerprint(id),
	}
	if v.Kind == settings.KindLocal {
		sheet.VaultPath = filepath.Clean(v.Config.Path)
		return sheet, nil
	}
	for _, account := range a.core.Snapshot().Accounts {
		if account.ID == v.Account {
			sheet.Service, sheet.ServiceTitle, sheet.AccountName = account.Service, account.ServiceTitle, account.Name
		}
	}
	if signIn, err := a.core.PCloudSignIn(v.Account); err == nil {
		sheet.PCloudHost = signIn.Hostname
		sheet.Region = "US"
		if signIn.Hostname == pcloud.HostEU {
			sheet.Region = "EU (Europe)"
		}
	}
	return sheet, nil
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
