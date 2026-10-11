// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build windows

package main

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	"golang.org/x/sys/windows/registry"
)

// Window backgrounds, shown before the page is drawn; the page uses the same
// colours (--bg in style.css).
var (
	darkBackground  = options.RGBA{R: 17, G: 19, B: 23, A: 255}
	lightBackground = options.RGBA{R: 243, G: 244, B: 246, A: 255}
)

// personalizeKey holds Windows' choice between light and dark apps
// (Settings → Personalisation → Colours).
const personalizeKey = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`

// windowsUsesDarkTheme reports whether Windows shows apps dark. Without the
// value Windows uses light, so that is the answer then.
func windowsUsesDarkTheme() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, personalizeKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	return err == nil && value == 0
}
