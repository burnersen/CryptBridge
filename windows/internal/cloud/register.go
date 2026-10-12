// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package cloud

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/burnersen/CryptBridge/windows/internal/rcd"
)

// maxQuestions limits the follow-up questions of one sign-in. rclone asks at
// most four for the supported services (measured); more would mean that it
// keeps rejecting an answer.
const maxQuestions = 20

// Question is a follow-up question of rclone that CryptBridge leaves to the
// user, e.g. which of several OneDrive drives to use.
type Question struct {
	Name      string   `json:"name"` // rclone's name, e.g. "config_driveid"
	Help      string   `json:"help"`
	Default   string   `json:"default"`
	Choices   []Choice `json:"choices"`
	Exclusive bool     `json:"exclusive"` // only one of the choices is allowed
	Password  bool     `json:"password"`
}

// Choice is one possible answer to a Question.
type Choice struct {
	Value string `json:"value"`
	Help  string `json:"help"`
}

// AskFunc asks the user a question and returns the answer, or an error if
// the user cancels.
type AskFunc func(ctx context.Context, q Question) (string, error)

// answer decides a follow-up question; ok false leaves it to the user.
type answer func(q Question) (reply string, ok bool)

// always answers a question with the same reply.
func always(reply string) answer {
	return func(Question) (string, bool) { return reply, true }
}

// onlyChoice takes the one choice offered; with several, the user chooses.
func onlyChoice(q Question) (string, bool) {
	if len(q.Choices) == 1 {
		return q.Choices[0].Value, true
	}
	return "", false
}

// commonAnswers decide the questions every service asks.
var commonAnswers = map[string]answer{
	// "Token already configured - replace it?": the token was just made.
	"config_refresh_token": always("false"),
}

// reply answers a question itself if it can, otherwise asks the user.
func (s Service) reply(ctx context.Context, q Question, ask AskFunc) (string, error) {
	decide, found := s.answers[q.Name]
	if !found {
		decide, found = commonAnswers[q.Name]
	}
	if found {
		if reply, ok := decide(q); ok {
			return reply, nil
		}
	}
	if ask == nil {
		return "", fmt.Errorf("rclone asked %q while setting up %s, which CryptBridge cannot answer", q.Name, s.Title)
	}
	return ask(ctx, q)
}

// configReply is rclone's answer to config/create and config/update in
// non-interactive mode: the next question, or an empty State when done.
type configReply struct {
	State  string        `json:"State"`
	Option *configOption `json:"Option"`
	Error  string        `json:"Error"`
}

// configOption is the question inside a configReply.
type configOption struct {
	Name       string `json:"Name"`
	Help       string `json:"Help"`
	DefaultStr string `json:"DefaultStr"`
	Examples   []struct {
		Value string `json:"Value"`
		Help  string `json:"Help"`
	} `json:"Examples"`
	Exclusive  bool `json:"Exclusive"`
	IsPassword bool `json:"IsPassword"`
}

// question turns rclone's option into a Question.
func (o configOption) question() Question {
	q := Question{Name: o.Name, Help: o.Help, Default: o.DefaultStr, Exclusive: o.Exclusive, Password: o.IsPassword}
	for _, example := range o.Examples {
		q.Choices = append(q.Choices, Choice{Value: example.Value, Help: example.Help})
	}
	return q
}

// Register creates the rclone remote name for an account of service, signed
// in with token (through the own app, if set), and goes through rclone's
// follow-up questions: CryptBridge answers the ones it knows, ask gets any
// other. It returns the remote's settings as rclone keeps them; Restore needs
// exactly these to register the account again later. On failure the remote
// is removed again.
func Register(ctx context.Context, client *rcd.Client, name string, service Service, token string, app App, ask AskFunc) (map[string]string, error) {
	parameters := map[string]string{"token": token}
	if app.IsSet() {
		parameters["client_id"] = app.ClientID
		parameters["client_secret"] = app.ClientSecret
	}
	if service.prepare != nil {
		found, err := service.prepare(ctx, client, token)
		if err != nil {
			return nil, err
		}
		maps.Copy(parameters, found)
	}
	maps.Copy(parameters, service.fixed)
	params, err := registerRemote(ctx, client, name, service, parameters, ask)
	if err != nil {
		removeRemote(context.WithoutCancel(ctx), client, name)
		return nil, err
	}
	return params, nil
}

// registerRemote creates the remote and answers rclone's questions.
func registerRemote(ctx context.Context, client *rcd.Client, name string, service Service, parameters map[string]string, ask AskFunc) (map[string]string, error) {
	removeRemote(ctx, client, name)
	var reply configReply
	request := map[string]any{
		"name":       name,
		"type":       service.Type,
		"parameters": parameters,
		"opt":        map[string]any{"nonInteractive": true, "obscure": true}, // all values are as typed
	}
	if err := client.Call(ctx, "config/create", request, &reply); err != nil {
		return nil, fmt.Errorf("set up the %s account: %w", service.Title, err)
	}
	if err := answerQuestions(ctx, client, name, service, reply, ask); err != nil {
		return nil, err
	}
	return ReadParams(ctx, client, name)
}

// answerQuestions goes through rclone's follow-up questions until rclone has
// none left. Each answer goes back with the state rclone gave the question.
func answerQuestions(ctx context.Context, client *rcd.Client, name string, service Service, reply configReply, ask AskFunc) error {
	for asked := 0; reply.State != ""; asked++ {
		if asked == maxQuestions {
			return fmt.Errorf("setting up the %s account did not finish after %d questions", service.Title, maxQuestions)
		}
		if reply.Error != "" {
			return fmt.Errorf("%s: %s", service.Title, reply.Error)
		}
		if reply.Option == nil {
			return fmt.Errorf("rclone stopped with an unexpected step while setting up %s", service.Title)
		}
		answer, err := service.reply(ctx, reply.Option.question(), ask)
		if err != nil {
			return err
		}
		request := map[string]any{
			"name":       name,
			"parameters": map[string]string{},
			"opt": map[string]any{
				"nonInteractive": true,
				"continue":       true,
				"state":          reply.State,
				"result":         answer,
			},
		}
		var next configReply
		if err := client.Call(ctx, "config/update", request, &next); err != nil {
			return fmt.Errorf("set up the %s account: %w", service.Title, err)
		}
		reply = next
	}
	return nil
}

// Restore registers a stored account again, e.g. after rclone was restarted,
// from the settings Register or ReadParams returned. rclone stores them
// first and then stops at its first question, which needs no answer: all
// answers are among the settings (measured: "Token already configured -
// replace it?"). The settings CryptBridge always sets for the service are
// added, so that accounts stored before a setting existed get it too.
func Restore(ctx context.Context, client *rcd.Client, name string, params map[string]string) error {
	serviceType := params["type"]
	if serviceType == "" || params["token"] == "" {
		return errors.New("the cloud account is not signed in")
	}
	parameters := maps.Clone(params)
	delete(parameters, "type")
	if service, err := ServiceOf(serviceType); err == nil {
		maps.Copy(parameters, service.fixed)
	}
	removeRemote(ctx, client, name)
	request := map[string]any{
		"name":       name,
		"type":       serviceType,
		"parameters": parameters,
		"opt":        map[string]any{"nonInteractive": true, "noObscure": true}, // as rclone stored them
	}
	if err := client.Call(ctx, "config/create", request, nil); err != nil {
		return fmt.Errorf("register the %s account: %w", serviceType, err)
	}
	return nil
}

// ReadParams returns the settings rclone keeps for the remote name. Among
// them is the token, which rclone renews by itself while it uses the
// account; Microsoft replaces the whole token on every renewal, so the
// stored copy must be updated from here.
func ReadParams(ctx context.Context, client *rcd.Client, name string) (map[string]string, error) {
	params := map[string]string{}
	if err := client.Call(ctx, "config/get", map[string]any{"name": name}, &params); err != nil {
		return nil, fmt.Errorf("read the settings of %s: %w", name, err)
	}
	if params["type"] == "" {
		return nil, fmt.Errorf("rclone does not know the account %s", name)
	}
	return params, nil
}

// removeRemote deletes the remote name from rclone's configuration. An
// error is ignored: a missing remote needs no deleting, and any other
// problem with rclone shows in the next call.
func removeRemote(ctx context.Context, client *rcd.Client, name string) {
	_ = client.Call(ctx, "config/delete", map[string]any{"name": name}, nil)
}
