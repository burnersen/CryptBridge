// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"testing"
	"time"
)

// TestSessionWatcherDeliversEvents sends the hidden window the messages
// Windows sends on Win+L, on unlocking and on signing out. Whether Windows
// really sends them can only be seen by pressing Win+L.
func TestSessionWatcherDeliversEvents(t *testing.T) {
	for round := 1; round <= 2; round++ { // the second round finds the window class already registered
		events := make(chan sessionEvent, 4)
		w, err := startSessionWatcher(func(e sessionEvent) { events <- e })
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		post := func(message, wParam uintptr) {
			if ok, _, err := procPostMessage.Call(w.hwnd, message, wParam, 0); ok == 0 {
				t.Fatalf("post message: %v", err)
			}
		}
		expect := func(want sessionEvent) {
			t.Helper()
			select {
			case got := <-events:
				if got != want {
					t.Fatalf("round %d: got event %d, want %d", round, got, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("round %d: event %d not delivered", round, want)
			}
		}

		post(wmWtsSessionChange, wtsSessionLock)
		expect(sessionLocked)
		post(wmWtsSessionChange, wtsSessionUnlock)
		expect(sessionUnlocked)
		post(wmEndSession, 0) // signing out was cancelled: nothing happens
		post(wmEndSession, 1)
		expect(sessionEnding)

		stopped := make(chan struct{})
		go func() { w.stop(); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Fatalf("round %d: watcher did not stop", round)
		}
	}
}
