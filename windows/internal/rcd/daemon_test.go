// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package rcd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestStartAttachStop runs the real rclone: start it hidden, attach to it a
// second time (as a new CryptBridge start would), then stop it and make sure
// the process and its state file are gone. Skipped when rclone is missing.
func TestStartAttachStop(t *testing.T) {
	exe, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not found in PATH")
	}
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "rcd.json")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	client, err := Start(ctx, Options{
		RcloneExe:  exe,
		StateFile:  stateFile,
		LogFile:    filepath.Join(dir, "rclone.log"),
		ConfigFile: filepath.Join(dir, "rclone.conf"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !processRunning(client.PID()) {
		t.Fatalf("process %d not running after Start", client.PID())
	}

	again, err := Attach(ctx, stateFile)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if again.PID() != client.PID() {
		t.Fatalf("Attach found process %d, want %d", again.PID(), client.PID())
	}

	if err := Stop(ctx, again, stateFile); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if processRunning(client.PID()) {
		t.Fatalf("process %d still running after Stop", client.PID())
	}
	if _, err := os.Stat(stateFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state file still there after Stop: %v", err)
	}
}

// TestAttachStaleState covers the state file of a process that no longer
// exists: Attach must report "nothing there" and clean the file up.
func TestAttachStaleState(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "rcd.json")
	// A process ID this high is never handed out in practice.
	if err := writeState(stateFile, State{PID: 0x7FFFFFF0, Port: 1, User: "x", Pass: "y"}); err != nil {
		t.Fatal(err)
	}
	_, err := Attach(context.Background(), stateFile)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Attach on stale state: got %v, want os.ErrNotExist", err)
	}
	if _, err := os.Stat(stateFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale state file not removed: %v", err)
	}
}

// TestAttachWrongProcess covers a port taken over by another program after
// a crash: the answer must come from the recorded PID, or Attach refuses.
func TestAttachWrongProcess(t *testing.T) {
	exe, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not found in PATH")
	}
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "rcd.json")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	client, err := Start(ctx, Options{RcloneExe: exe, StateFile: stateFile, ConfigFile: filepath.Join(dir, "rclone.conf")})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer Stop(ctx, client, stateFile)

	state, err := readState(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	// Pretend the state belongs to this test process: alive, but not rclone.
	state.PID = os.Getpid()
	forged := filepath.Join(dir, "forged.json")
	if err := writeState(forged, state); err != nil {
		t.Fatal(err)
	}
	if _, err := Attach(ctx, forged); err == nil {
		t.Fatal("Attach accepted a port answered by a different process")
	}
}
