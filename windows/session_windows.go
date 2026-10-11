// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"errors"
	"fmt"
	goruntime "runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sessionWatcher tells CryptBridge when Windows is locked (Win+L, screen
// lock), unlocked, or when the user signs out or shuts down.
//
// Windows sends these messages to a window, and only to the thread that
// created it. So the watcher owns a hidden window on a thread of its own
// with its own message loop, like the tray icon. It is a normal top-level
// window, never shown: Windows sends the sign-out messages only to those.
type sessionWatcher struct {
	onEvent func(sessionEvent)
	hwnd    uintptr
	done    chan struct{}
}

type sessionEvent int

const (
	sessionLocked   sessionEvent = iota // Win+L or the screen lock
	sessionUnlocked                     // the user is back
	sessionEnding                       // signing out or shutting down
)

// Window messages and constants from the Windows headers.
const (
	wmDestroy          = 0x0002
	wmClose            = 0x0010
	wmQueryEndSession  = 0x0011
	wmEndSession       = 0x0016
	wmWtsSessionChange = 0x02B1
	wtsSessionLock     = 0x7
	wtsSessionUnlock   = 0x8
	notifyForThisSess  = 0
	wsExToolWindow     = 0x00000080
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassEx   = user32.NewProc("RegisterClassExW")
	procCreateWindowEx    = user32.NewProc("CreateWindowExW")
	procDefWindowProc     = user32.NewProc("DefWindowProcW")
	procGetMessage        = user32.NewProc("GetMessageW")
	procTranslateMessage  = user32.NewProc("TranslateMessage")
	procDispatchMessage   = user32.NewProc("DispatchMessageW")
	procPostMessage       = user32.NewProc("PostMessageW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
	procPostQuitMessage   = user32.NewProc("PostQuitMessage")
	procGetModuleHandle   = kernel32.NewProc("GetModuleHandleW")
	procWTSRegister       = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnregister     = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
	sessionWindowProcAddr = windows.NewCallback(sessionWindowProc) // created once: Windows keeps it for good
)

type wndClassEx struct {
	size, style            uint32
	wndProc                uintptr
	clsExtra, wndExtra     int32
	instance, icon, cursor uintptr
	background             uintptr
	menuName, className    *uint16
	iconSm                 uintptr
}

type winMsg struct {
	hwnd           uintptr
	message        uint32
	wParam, lParam uintptr
	time           uint32
	ptX, ptY       int32
	private        uint32
}

// The window procedure is a plain function; it finds its watcher here.
var (
	watchersMu sync.Mutex
	watchers   = map[uintptr]*sessionWatcher{}
)

// startSessionWatcher creates the hidden window and starts listening.
// onEvent is called for sessionLocked and sessionUnlocked on a goroutine of
// its own; for sessionEnding it is called directly and must finish quickly,
// because Windows ends the program soon after.
func startSessionWatcher(onEvent func(sessionEvent)) (*sessionWatcher, error) {
	w := &sessionWatcher{onEvent: onEvent, done: make(chan struct{})}
	created := make(chan error, 1)
	go func() {
		goruntime.LockOSThread() // the window's messages arrive only on this thread
		defer close(w.done)
		if err := w.createWindow(); err != nil {
			created <- err
			return
		}
		created <- nil
		w.messageLoop()
	}()
	if err := <-created; err != nil {
		return nil, err
	}
	return w, nil
}

// stop closes the hidden window and waits for its thread to end.
func (w *sessionWatcher) stop() {
	_, _, _ = procPostMessage.Call(w.hwnd, wmClose, 0, 0)
	<-w.done
}

func (w *sessionWatcher) createWindow() error {
	instance, _, _ := procGetModuleHandle.Call(0)
	className, err := windows.UTF16PtrFromString("CryptBridgeSessionWatcher")
	if err != nil {
		return err
	}
	class := wndClassEx{wndProc: sessionWindowProcAddr, instance: instance, className: className}
	class.size = uint32(unsafe.Sizeof(class))
	// A second watcher in the same process finds the class already there.
	if atom, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); atom == 0 && !errors.Is(err, windows.ERROR_CLASS_ALREADY_EXISTS) {
		return fmt.Errorf("register window class: %w", err)
	}
	hwnd, _, err := procCreateWindowEx.Call(wsExToolWindow, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("create window: %w", err)
	}
	w.hwnd = hwnd
	watchersMu.Lock()
	watchers[hwnd] = w
	watchersMu.Unlock()
	if ok, _, err := procWTSRegister.Call(hwnd, notifyForThisSess); ok == 0 {
		_, _, _ = procDestroyWindow.Call(hwnd)
		return fmt.Errorf("register for Windows lock messages: %w", err)
	}
	return nil
}

func (w *sessionWatcher) messageLoop() {
	var m winMsg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0: WM_QUIT, -1: error
			return
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func sessionWindowProc(hwnd, message, wParam, lParam uintptr) uintptr {
	watchersMu.Lock()
	w := watchers[hwnd]
	watchersMu.Unlock()
	if w == nil {
		r, _, _ := procDefWindowProc.Call(hwnd, message, wParam, lParam)
		return r
	}

	switch message {
	case wmWtsSessionChange:
		switch wParam {
		case wtsSessionLock:
			go w.onEvent(sessionLocked)
		case wtsSessionUnlock:
			go w.onEvent(sessionUnlocked)
		}
		return 0
	case wmQueryEndSession:
		return 1 // never stand in the way of signing out
	case wmEndSession:
		if wParam != 0 { // the session really ends
			w.onEvent(sessionEnding)
		}
		return 0
	case wmClose:
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		_, _, _ = procWTSUnregister.Call(hwnd)
		watchersMu.Lock()
		delete(watchers, hwnd)
		watchersMu.Unlock()
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}
