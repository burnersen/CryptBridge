// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// CryptBridge shows rclone-crypt encrypted vaults — in pCloud or in folders
// of this PC — as Windows drive letters. It lives in the system tray; the
// window is only for setup and status.
package main

import (
	"embed"
	"log"
	"os"
	"slices"

	"github.com/burnersen/CryptBridge/windows/internal/appdir"
	"github.com/burnersen/CryptBridge/windows/internal/engine"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is set by the build script from the VERSION file.
var version = "dev"

const (
	// singleInstanceID must never change, or a new version would not find
	// an older one that is still running.
	singleInstanceID = "CryptBridge-3c9f2d4e-tray"

	// argBackground starts CryptBridge without showing the window (used by
	// the Windows autostart entry).
	argBackground = "--background"

	windowWidth  = 760
	windowHeight = 720
)

func main() {
	// The window runs in a child process watched by this one, because a
	// crash of the Windows web view ends the whole process (see guardian.go).
	if !isWindowProcess(os.Args) {
		if exitCode, guarded := runGuardian(); guarded {
			os.Exit(exitCode)
		}
		// No guardian possible: open the window in this process instead.
	}

	paths, err := appdir.Resolve()
	if err != nil {
		log.Fatal(err)
	}
	core := engine.New(paths, version)
	app := NewApp(core, paths, slices.Contains(os.Args, argBackground))
	background := lightBackground
	if windowsUsesDarkTheme() {
		background = darkBackground
	}

	err = wails.Run(&options.App{
		Title:             "CryptBridge",
		Width:             windowWidth,
		Height:            windowHeight,
		MinWidth:          600,
		MinHeight:         560,
		StartHidden:       app.startHidden,
		HideWindowOnClose: true, // the tray keeps running; Quit is in its menu
		BackgroundColour:  &background,
		AssetServer:       &assetserver.Options{Assets: assets},
		OnStartup:         app.startup,
		OnShutdown:        app.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               singleInstanceID,
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		Windows: &windows.Options{Theme: windows.SystemDefault}, // the saved choice follows at start
		Bind:    []any{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
