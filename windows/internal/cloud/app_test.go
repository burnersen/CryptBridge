// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import "testing"

func mustService(t *testing.T, serviceType string) Service {
	t.Helper()
	service, err := ServiceOf(serviceType)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestNewAppTrimsPastedCodes(t *testing.T) {
	app, err := NewApp(mustService(t, PCloud), "  DnONSzyJXpm ", "\tAbCdEfGhIjKlMnOpQrStUvWxYz12\n")
	if err != nil || app.ClientID != "DnONSzyJXpm" || app.ClientSecret != "AbCdEfGhIjKlMnOpQrStUvWxYz12" || !app.IsSet() {
		t.Fatalf("got %+v, %v; want trimmed codes", app, err)
	}
	google := "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"
	app, err = NewApp(mustService(t, Drive), google, "GOCSPX-AbCdEfGhIjKlMnOpQrStUvWx")
	if err != nil || app.ClientID != google {
		t.Fatalf("Google codes: got %+v, %v", app, err)
	}
}

func TestNewAppEmptyCodes(t *testing.T) {
	for _, serviceType := range []string{PCloud, Dropbox, OneDrive} {
		if app, err := NewApp(mustService(t, serviceType), " ", ""); err != nil || app.IsSet() {
			t.Errorf("%s, empty codes: got %+v, %v; want rclone's app", serviceType, app, err)
		}
	}
	if _, err := NewApp(mustService(t, Drive), "", ""); err == nil {
		t.Error("Google Drive must not accept rclone's app")
	}
}

func TestNewAppRejectsBadCodes(t *testing.T) {
	pcloudService := mustService(t, PCloud)
	for _, bad := range [][2]string{
		{"DnONSzyJXpm", ""},             // secret missing
		{"", "AbCdEfGhIjKl"},            // ID missing
		{"--config=x", "AbCdEfGhIjKl"},  // would be read as an option
		{"Dn ON", "AbCdEfGhIjKl"},       // space inside
		{"DnONSzyJXpm", "abc\"; rm -x"}, // not a code at all
		{"abc", "AbCdEfGhIjKl"},         // too short
	} {
		if _, err := NewApp(pcloudService, bad[0], bad[1]); err == nil {
			t.Errorf("NewApp(%q, %q) accepted", bad[0], bad[1])
		}
	}
	if _, err := NewApp(mustService(t, Drive), "my-project-123456", "GOCSPX-AbCdEfGh"); err == nil {
		t.Error("a Google project name was accepted as client ID")
	}
	if _, err := NewApp(mustService(t, OneDrive), "b15665d9-eda6-4092-8539-0eec376afd59", "secret1234"); err == nil {
		t.Error("OneDrive must not take an own app")
	}
}
