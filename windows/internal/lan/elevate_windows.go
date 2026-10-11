// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package lan

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// shellExecuteInfo is Windows' SHELLEXECUTEINFOW. Go lays the fields out
// like C does, padding included.
type shellExecuteInfo struct {
	size          uint32
	mask          uint32
	window        windows.HWND
	verb          *uint16
	file          *uint16
	parameters    *uint16
	directory     *uint16
	show          int32
	instApp       windows.Handle
	idList        uintptr
	class         *uint16
	keyClass      windows.Handle
	hotKey        uint32
	iconOrMonitor windows.Handle
	process       windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040 // keep the process handle, to wait for its end
	seeMaskNoAsync        = 0x00000100 // return only after the process was started
)

// startElevated runs a PowerShell script as administrator: Windows asks the
// user first ("Do you want to allow this app to make changes?"). It waits
// for the script to end and returns its exit code.
func startElevated(script string, timeout time.Duration) (uint32, error) {
	exe, err := powerShell()
	if err != nil {
		return 0, err
	}
	// ShellExecuteEx may hand the work to parts of Windows that need COM on
	// the calling thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil {
		defer windows.CoUninitialize()
	}

	info := shellExecuteInfo{
		mask:       seeMaskNoCloseProcess | seeMaskNoAsync,
		verb:       windows.StringToUTF16Ptr("runas"),
		file:       windows.StringToUTF16Ptr(exe),
		parameters: windows.StringToUTF16Ptr(strings.Join(append([]string{"-WindowStyle", "Hidden"}, powerShellArgs(script)...), " ")),
		show:       windows.SW_HIDE,
	}
	info.size = uint32(unsafe.Sizeof(info))
	if ok, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return 0, ErrDeclined
		}
		return 0, fmt.Errorf("start PowerShell as administrator: %w", callErr)
	}
	if info.process == 0 {
		return 0, errors.New("start PowerShell as administrator: Windows returned no process")
	}
	defer windows.CloseHandle(info.process)

	event, err := windows.WaitForSingleObject(info.process, uint32(timeout.Milliseconds()))
	switch {
	case err != nil:
		return 0, fmt.Errorf("wait for PowerShell: %w", err)
	case event == uint32(windows.WAIT_TIMEOUT):
		return 0, fmt.Errorf("PowerShell did not finish within %s", timeout)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err != nil {
		return 0, fmt.Errorf("read PowerShell's result: %w", err)
	}
	return code, nil
}
