// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

//go:build windows

package drive

import (
	"errors"
	"fmt"
	"io/fs"

	"golang.org/x/sys/windows"
)

// Space is the size of a disk.
type Space struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"` // what this user may still write
}

// DiskSpace reports the size of the disk that holds path.
func DiskSpace(path string) (Space, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Space{}, err
	}
	var freeForUser, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(name, &freeForUser, &total, &totalFree); err != nil {
		return Space{}, fmt.Errorf("read the free space of %s: %w", path, missingAsNotExist(err))
	}
	return Space{Total: int64(total), Used: int64(total - totalFree), Free: int64(freeForUser)}, nil
}

// FolderID identifies a folder. The same ID means the very same folder on
// the very same disk, also after the disk was unplugged and plugged in again;
// a folder that was moved away and replaced by a new one of the same name,
// or the same path on another USB drive, has a different ID.
type FolderID struct {
	Volume uint32
	Index  uint64
}

// IdentifyFolder returns the ID of the folder at path. If the folder or its
// disk is not there, the error matches fs.ErrNotExist.
func IdentifyFolder(path string) (FolderID, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return FolderID{}, err
	}
	// No access rights are needed to read the ID; backup semantics allow
	// opening a folder.
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return FolderID{}, fmt.Errorf("open %s: %w", path, missingAsNotExist(err))
	}
	defer windows.CloseHandle(handle)

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return FolderID{}, fmt.Errorf("identify %s: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return FolderID{}, fmt.Errorf("%s is a file, not a folder", path)
	}
	return FolderID{
		Volume: info.VolumeSerialNumber,
		Index:  uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
	}, nil
}

// missingAsNotExist makes the ways Windows reports a missing folder or disk
// match fs.ErrNotExist, including a drive letter that is gone with its USB
// drive and a card reader without a card.
func missingAsNotExist(err error) error {
	for _, missing := range []windows.Errno{
		windows.ERROR_FILE_NOT_FOUND,
		windows.ERROR_PATH_NOT_FOUND,
		windows.ERROR_INVALID_DRIVE,
		windows.ERROR_NOT_READY,
		windows.ERROR_BAD_NETPATH,
		windows.ERROR_DEV_NOT_EXIST,
	} {
		if errors.Is(err, missing) {
			return fmt.Errorf("%w (%v)", fs.ErrNotExist, err)
		}
	}
	return err
}
