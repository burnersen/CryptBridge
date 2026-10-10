// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build windows

package settings

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// entropy ties the encrypted data to CryptBridge: another program of the
// same user has to know this value to decrypt it. It is no secret (this is
// open source), only a guard against accidental reuse.
var entropy = []byte("CryptBridge settings")

// protect encrypts data with the key of the current Windows user (DPAPI).
func protect(data []byte) ([]byte, error) {
	return dpapi(data, true)
}

// unprotect reverses protect. It fails for another Windows account.
func unprotect(data []byte) ([]byte, error) {
	return dpapi(data, false)
}

func dpapi(data []byte, encrypt bool) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(data))}
	if len(data) > 0 {
		in.Data = &data[0]
	}
	extra := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var out windows.DataBlob

	var err error
	if encrypt {
		err = windows.CryptProtectData(&in, nil, &extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, &extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
