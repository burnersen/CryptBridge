// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// authPort is where "rclone authorize" waits for the browser (rclone 1.75.1).
const authPort = "127.0.0.1:53682"

// A cancelled sign-in must end rclone: a forgotten "rclone authorize" keeps
// the port, and every later sign-in fails (seen 2026-10-12). The browser is
// kept closed through rclone's environment variable.
func TestAuthorizeCancelFreesThePort(t *testing.T) {
	exe, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not found in PATH")
	}
	t.Setenv("RCLONE_AUTH_NO_OPEN_BROWSER", "true")
	service := mustService(t, OneDrive)
	ctx, cancel := context.WithCancel(context.Background())
	links := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		_, err := Authorize(ctx, exe, service, App{}, func(link string) { links <- link })
		done <- err
	}()
	select {
	case <-links:
	case err := <-done:
		t.Fatalf("sign-in ended before it showed its link: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("no sign-in link within 20 s")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("err = %v, want the cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Authorize did not return within 10 s after cancelling")
	}
	waitPortFree(t)
}

// A port held by something else ends the sign-in with words that say so,
// not with "exit status 2".
func TestAuthorizeExplainsABusyPort(t *testing.T) {
	exe, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not found in PATH")
	}
	t.Setenv("RCLONE_AUTH_NO_OPEN_BROWSER", "true")
	listener, err := net.Listen("tcp", authPort)
	if err != nil {
		t.Skipf("%s is in use already: %v", authPort, err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = Authorize(ctx, exe, mustService(t, OneDrive), App{}, nil)
	if err == nil || !strings.Contains(err.Error(), "port 53682") {
		t.Fatalf("err = %v, want the explanation of the busy port", err)
	}
}

// When CryptBridge ends while a sign-in waits, Windows must end rclone too.
// Ending CryptBridge closes its handle to the job, as release does here.
func TestEndWithThisProgram(t *testing.T) {
	exe, err := exec.LookPath("rclone")
	if err != nil {
		t.Skip("rclone not found in PATH")
	}
	cmd := exec.Command(exe, "authorize", OneDrive, "--auth-no-open-browser")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(ended)
	}()
	release, err := endWithThisProgram(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	time.Sleep(time.Second) // rclone is waiting for the browser now
	release()
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("rclone kept running after its job was closed")
	}
	waitPortFree(t)
}

func waitPortFree(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if conn, err := net.DialTimeout("tcp", authPort, 200*time.Millisecond); err != nil {
			return
		} else {
			conn.Close()
		}
	}
	t.Fatalf("%s is still in use: rclone authorize kept running", authPort)
}
