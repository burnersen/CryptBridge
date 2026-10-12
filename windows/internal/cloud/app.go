// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// App is the user's own app at a cloud service: the client ID and client
// secret rclone signs in with instead of its built-in ones.
type App struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"` // a secret
}

// IsSet reports whether an own app is configured.
func (a App) IsSet() bool {
	return a.ClientID != ""
}

// appCodePattern: letters, digits, ".", "_" and "-" (Google: "123-abc.apps.
// googleusercontent.com" and "GOCSPX-…"; pCloud and Dropbox: letters and
// digits). Never a leading "-", which rclone would read as an option.
var appCodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{3,127}$`)

// NewApp checks the codes of an own app for service, ignoring spaces around
// pasted text. Two empty codes mean "use rclone's app" where that is allowed.
func NewApp(service Service, clientID, clientSecret string) (App, error) {
	app := App{ClientID: strings.TrimSpace(clientID), ClientSecret: strings.TrimSpace(clientSecret)}
	empty := app.ClientID == "" && app.ClientSecret == ""
	switch {
	case empty && service.App == AppRequired:
		return App{}, fmt.Errorf("%s needs your own app: enter its client ID and client secret", service.Title)
	case empty:
		return App{}, nil
	case service.App == AppNotOffered:
		return App{}, fmt.Errorf("%s works with rclone's app only; leave the client ID and secret empty", service.Title)
	case app.ClientID == "" || app.ClientSecret == "":
		return App{}, errors.New("enter both the client ID and the client secret of your app")
	case !appCodePattern.MatchString(app.ClientID) || !strings.HasSuffix(app.ClientID, service.clientIDSuffix):
		return App{}, errors.New("the client ID does not look right; copy it again from your app" + suffixHint(service))
	case !appCodePattern.MatchString(app.ClientSecret):
		return App{}, errors.New("the client secret does not look right; copy it again from your app")
	}
	return app, nil
}

// suffixHint tells how the client IDs of the service end, if they all do.
func suffixHint(service Service) string {
	if service.clientIDSuffix == "" {
		return ""
	}
	return fmt.Sprintf(" (it ends with %s)", service.clientIDSuffix)
}
