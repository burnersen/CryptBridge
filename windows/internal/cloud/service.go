// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package cloud signs in to cloud services through rclone and registers the
// accounts with the running rclone process. Most services go the same way:
// "rclone authorize" opens the service's sign-in page in the browser and
// returns a token; the token then goes to the running rclone, which may ask a
// few follow-up questions through its non-interactive configuration. MEGA
// has no sign-in page: it takes the e-mail address and password instead and
// keeps a session (see password.go). No terminal is involved, and rclone
// keeps its configuration in memory only.
//
// A new service needs one entry in the table below, plus answers for the
// follow-up questions rclone asks about it.
package cloud

import (
	"context"
	"fmt"

	"github.com/burnersen/CryptBridge/windows/internal/pcloud"
	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// The services, by rclone's names of their backends.
const (
	PCloud   = "pcloud"
	Drive    = "drive"
	OneDrive = "onedrive"
	Dropbox  = "dropbox"
	Mega     = "mega"
)

// SignInWay tells how the user signs in to a service.
type SignInWay string

const (
	// SignInBrowser: on the service's own page in the browser ("rclone
	// authorize"), which gives a token.
	SignInBrowser SignInWay = "browser"
	// SignInPassword: with e-mail address, password and possibly a
	// two-factor code typed into CryptBridge, which give a session.
	SignInPassword SignInWay = "password"
)

// The settings that hold a sign-in in rclone's configuration.
var (
	tokenSettings   = []string{"token"}
	sessionSettings = []string{megaSessionID, megaMasterKey}
)

// AppRule tells whether the user can or must bring an own app (client ID and
// client secret) for signing in.
type AppRule string

const (
	// AppNotOffered: only rclone's app is used. Personal Microsoft accounts
	// can no longer register apps (rclone forum, February 2026).
	AppNotOffered AppRule = "none"
	// AppOptional: rclone's app works; an own one avoids its shared limits.
	AppOptional AppRule = "optional"
	// AppRequired: rclone's app is being retired. Google wants to charge for
	// rclone's shared Google Drive app during 2026 (rclone forum, July 2026).
	AppRequired AppRule = "required"
)

// Service is a cloud service CryptBridge signs in to.
type Service struct {
	Type   string    `json:"type"`  // rclone's backend name
	Title  string    `json:"title"` // shown in the window
	App    AppRule   `json:"app"`
	SignIn SignInWay `json:"signIn"`
	// RedirectURL is the address an own app must allow, as rclone prints it
	// for the service (measured with rclone 1.75.1).
	RedirectURL string `json:"redirectUrl"`
	// CaseInsensitive: the service does not tell upper from lower case in
	// file names (rclone.org/overview), so two names that differ only in
	// case land on one file.
	CaseInsensitive bool `json:"caseInsensitive"`
	// NoChangeReports: the service does not tell rclone about changes made
	// elsewhere, so the drive must look at its folders again soon.
	NoChangeReports bool `json:"-"`

	// signedInBy are the settings that hold the sign-in; an account without
	// them is signed out.
	signedInBy []string
	// clientIDSuffix is how every client ID of the service ends; it catches
	// a project name or secret pasted in the wrong field.
	clientIDSuffix string
	// answers decide rclone's follow-up questions, by question name.
	answers map[string]answer
	// fixed are rclone settings CryptBridge always sets for the service,
	// also on accounts registered before they were set.
	fixed map[string]string
	// prepare finds settings that rclone cannot ask for itself; it runs
	// before the account is registered.
	prepare func(ctx context.Context, client *rcd.Client, token string) (map[string]string, error)
}

// services is the table of supported services, in the order shown.
var services = []Service{
	{
		Type: PCloud, Title: "pCloud", App: AppOptional,
		SignIn: SignInBrowser, signedInBy: tokenSettings,
		RedirectURL: pcloud.RedirectURL,
		prepare:     pcloudRegion,
	},
	{
		Type: Drive, Title: "Google Drive", App: AppRequired,
		SignIn: SignInBrowser, signedInBy: tokenSettings,
		RedirectURL:    "http://127.0.0.1:53682/",
		clientIDSuffix: ".apps.googleusercontent.com",
		answers: map[string]answer{
			"config_change_team_drive": always("false"), // the user's own drive, not a shared one
		},
	},
	{
		Type: OneDrive, Title: "OneDrive", App: AppNotOffered,
		SignIn: SignInBrowser, signedInBy: tokenSettings,
		CaseInsensitive: true,
		answers: map[string]answer{
			"config_type":     always("onedrive"), // the account's own OneDrive (personal or business)
			"config_driveid":  onlyChoice,         // with several drives, the user chooses
			"config_drive_ok": always("true"),
		},
	},
	{
		Type: Dropbox, Title: "Dropbox", App: AppOptional,
		SignIn: SignInBrowser, signedInBy: tokenSettings,
		RedirectURL:     "http://localhost:53682/",
		CaseInsensitive: true,
		fixed: map[string]string{
			// rclone's Dropbox backend collects uploads in a "batcher", which
			// ends when rclone drops any vault or key check built on the same
			// folder from its cache (crypt passes the shutdown on). Every later
			// upload then fails with "batcher is shutting down" (seen
			// 2026-10-12, rclone 1.75.1). Without batching nothing can end:
			// small files go up one by one, which Dropbox may slow down.
			"batch_mode": "off",
		},
	},
	{
		// MEGA has no apps for others to bring, and it tells upper from lower
		// case (rclone.org/overview). rclone's MEGA backend has no way to
		// report changes (no ChangeNotify in backend/mega, rclone 1.75.2).
		Type: Mega, Title: "MEGA", App: AppNotOffered,
		SignIn: SignInPassword, signedInBy: sessionSettings,
		NoChangeReports: true,
	},
}

// HasSignIn reports whether rclone's settings of an account, as Register or
// ReadParams return them, hold a sign-in. A service this CryptBridge does not
// know counts as signed in with a token, like most services.
func HasSignIn(params map[string]string) bool {
	keys := tokenSettings
	if service, err := ServiceOf(params["type"]); err == nil {
		keys = service.signedInBy
	}
	for _, key := range keys {
		if params[key] == "" {
			return false
		}
	}
	return true
}

// Services returns the supported services, in the order shown.
func Services() []Service {
	list := make([]Service, len(services))
	copy(list, services)
	return list
}

// ServiceOf returns the service with rclone's backend name serviceType.
func ServiceOf(serviceType string) (Service, error) {
	for _, s := range services {
		if s.Type == serviceType {
			return s, nil
		}
	}
	return Service{}, fmt.Errorf("unknown cloud service %q", serviceType)
}

// pcloudRegion finds the pCloud server of the account: rclone cannot tell
// the region from the token, and only the region's own server accepts it.
func pcloudRegion(ctx context.Context, client *rcd.Client, token string) (map[string]string, error) {
	host, err := pcloud.FindRegion(ctx, client, token)
	if err != nil {
		return nil, err
	}
	return map[string]string{"hostname": host}, nil
}
