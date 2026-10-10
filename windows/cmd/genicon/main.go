// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// genicon writes the program icon (build/appicon.png). Wails turns it into
// the .ico of the program when building. Run it from the windows folder:
//
//	go run ./cmd/genicon
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/burnersen/CryptBridge/windows/internal/icon"
)

// appIconSize is the size Wails expects for its source image.
const appIconSize = 1024

func main() {
	data, err := icon.AppPNG(appIconSize)
	if err != nil {
		log.Fatal(err)
	}
	target := filepath.Join("build", "appicon.png")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s (%d bytes)", target, len(data))
}
