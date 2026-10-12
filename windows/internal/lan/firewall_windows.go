// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package lan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// The Windows firewall lets nothing from the network in by default.
// CryptBridge's rule lets in exactly what the LAN access needs: the
// connections to its port, for rclone.exe only, from devices of the same
// network, and only while Windows counts the network as a private (home)
// network — never in a hotel or café network.

const (
	// ruleName identifies CryptBridge's rule; Windows shows ruleDisplayName.
	ruleName        = "CryptBridge-LAN"
	ruleDisplayName = "CryptBridge LAN access (WebDAV)"
	ruleDescription = "Lets devices of your home network open the drives that CryptBridge shares. " +
		"Created by CryptBridge; it is removed when you turn the LAN access off."

	// firewallTimeout limits a look at the firewall; PowerShell needs about
	// two seconds to start (measured).
	firewallTimeout = 30 * time.Second

	// elevatedTimeout limits the change of the rule, including the time the
	// user takes to answer Windows' question.
	elevatedTimeout = 5 * time.Minute
)

// ErrDeclined: the user said no when Windows asked for permission.
var ErrDeclined = errors.New("Windows did not get permission to change the firewall")

// Network is a network connection as Windows classifies it.
type Network struct {
	Name     string `json:"name"`
	Adapter  string `json:"adapter"`
	Category string `json:"category"` // "Private", "Public" or "DomainAuthenticated"
}

// Rule is another firewall rule that lets rclone in.
type Rule struct {
	ID      string `json:"id"` // Windows' own name of the rule, unique
	Name    string `json:"name"`
	Profile string `json:"profile"` // the networks it applies to, e.g. "Public" or "Private, Public"
	Port    string `json:"port"`    // e.g. "Any" or "8080"
}

// Firewall is what the Windows firewall does about the LAN access.
type Firewall struct {
	RuleFound  bool      `json:"ruleFound"`
	RuleFits   bool      `json:"ruleFits"` // the rule lets the home network in on the port, for this rclone
	RulePort   string    `json:"rulePort"`
	PrivateOff bool      `json:"privateOff"` // the firewall is off for home networks: nothing to allow
	Blocking   []string  `json:"blocking"`   // rules that block rclone; they win over any rule that allows
	Others     []Rule    `json:"others"`     // other rules that let rclone in — Windows creates them when rclone is allowed once
	Networks   []Network `json:"networks"`
}

// firewallAnswer is what the PowerShell script of ReadFirewall prints.
type firewallAnswer struct {
	Rule *struct {
		Enabled   bool   `json:"enabled"`
		Action    string `json:"action"`
		Direction string `json:"direction"`
		Profile   string `json:"profile"`
		Protocol  string `json:"protocol"`
		Port      string `json:"port"`
		Program   string `json:"program"`
		Remote    string `json:"remote"`
	} `json:"rule"`
	PrivateEnabled bool      `json:"privateEnabled"`
	Blocking       []string  `json:"blocking"`
	Others         []Rule    `json:"others"`
	Networks       []Network `json:"networks"`
}

// ReadFirewall looks at CryptBridge's rule, at the other rules about rclone
// and at how Windows classifies the networks. The rule fits if it lets the
// home network in on exactly the given ports. It needs no administrator
// rights.
func ReadFirewall(ctx context.Context, ports []int, program string) (Firewall, error) {
	ctx, cancel := context.WithTimeout(ctx, firewallTimeout)
	defer cancel()
	script := `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false
$program = ` + psQuote(program) + `
$out = [ordered]@{ rule = $null; privateEnabled = $true; blocking = @(); others = @(); networks = @() }
$rule = Get-NetFirewallRule -Name ` + psQuote(ruleName) + ` -ErrorAction SilentlyContinue
if ($rule) {
  $ports = $rule | Get-NetFirewallPortFilter
  $out.rule = [ordered]@{
    enabled = ($rule.Enabled.ToString() -eq 'True')
    action = $rule.Action.ToString()
    direction = $rule.Direction.ToString()
    profile = $rule.Profile.ToString()
    protocol = [string]$ports.Protocol
    port = ($ports.LocalPort -join ',')
    program = [string]($rule | Get-NetFirewallApplicationFilter).Program
    remote = (($rule | Get-NetFirewallAddressFilter).RemoteAddress -join ',')
  }
}
$out.privateEnabled = ((Get-NetFirewallProfile -Name Private).Enabled.ToString() -eq 'True')
foreach ($filter in @(Get-NetFirewallApplicationFilter -Program $program -ErrorAction SilentlyContinue)) {
  $other = $filter | Get-NetFirewallRule
  if ($other.Name -eq ` + psQuote(ruleName) + ` -or $other.Enabled.ToString() -ne 'True' -or $other.Direction.ToString() -ne 'Inbound') { continue }
  if ($other.Action.ToString() -eq 'Block') { $out.blocking += $other.DisplayName; continue }
  $out.others += [pscustomobject]@{ id = $other.Name; name = $other.DisplayName; profile = $other.Profile.ToString()
    port = (($other | Get-NetFirewallPortFilter).LocalPort -join ',') }
}
$out.networks = @(Get-NetConnectionProfile -ErrorAction SilentlyContinue | ForEach-Object {
  [ordered]@{ name = $_.Name; adapter = $_.InterfaceAlias; category = $_.NetworkCategory.ToString() } })
$out | ConvertTo-Json -Compress -Depth 4`

	output, err := runPowerShell(ctx, script)
	if err != nil {
		return Firewall{}, fmt.Errorf("look at the Windows firewall: %w", err)
	}
	var answer firewallAnswer
	if err := json.Unmarshal(output, &answer); err != nil {
		return Firewall{}, fmt.Errorf("look at the Windows firewall: unexpected answer: %w", err)
	}
	state := Firewall{
		PrivateOff: !answer.PrivateEnabled,
		Blocking:   nonNil(answer.Blocking),
		Others:     answer.Others,
		Networks:   answer.Networks,
	}
	if state.Others == nil {
		state.Others = []Rule{}
	}
	if state.Networks == nil {
		state.Networks = []Network{}
	}
	if rule := answer.Rule; rule != nil {
		state.RuleFound = true
		state.RulePort = rule.Port
		state.RuleFits = rule.Enabled && rule.Action == "Allow" && rule.Direction == "Inbound" &&
			rule.Profile == "Private" && rule.Protocol == "TCP" && samePorts(rule.Port, ports) &&
			strings.EqualFold(filepath.Clean(rule.Program), filepath.Clean(program)) && rule.Remote == "LocalSubnet"
	}
	return state, nil
}

// portList writes ports the way the rule holds them, e.g. "8080,8081":
// sorted, each once.
func portList(ports []int) string {
	sorted := slices.Clone(ports)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	texts := make([]string, len(sorted))
	for i, port := range sorted {
		texts[i] = strconv.Itoa(port)
	}
	return strings.Join(texts, ",")
}

// samePorts reports whether the ports of a rule, as Windows lists them, are
// exactly the wanted ports, in any order.
func samePorts(listed string, ports []int) bool {
	var found []int
	for _, text := range strings.Split(listed, ",") {
		port, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			return false // "Any", a range or nothing: not the rule CryptBridge makes
		}
		found = append(found, port)
	}
	return len(ports) > 0 && portList(found) == portList(ports)
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// AllowThroughFirewall creates CryptBridge's rule for the ports of the LAN
// accesses and the given rclone.exe, replacing an older one. Windows asks
// the user for permission first; ErrDeclined means the user said no.
func AllowThroughFirewall(ports []int, program string) error {
	if len(ports) == 0 {
		return errors.New("no LAN access is on, so there is no port to allow")
	}
	for _, port := range ports {
		if err := CheckPort(port); err != nil {
			return err
		}
	}
	if !filepath.IsAbs(program) {
		return fmt.Errorf("rclone's path %q is not complete", program)
	}
	return runElevated(`Remove-NetFirewallRule -Name ` + psQuote(ruleName) + ` -ErrorAction SilentlyContinue
New-NetFirewallRule -Name ` + psQuote(ruleName) + ` -DisplayName ` + psQuote(ruleDisplayName) +
		` -Description ` + psQuote(ruleDescription) + ` -Direction Inbound -Action Allow -Protocol TCP -LocalPort ` +
		portList(ports) + ` -Profile Private -RemoteAddress LocalSubnet -Program ` + psQuote(program) + ` | Out-Null`)
}

// RemoveFirewallRule removes CryptBridge's rule. Windows asks the user for
// permission first; ErrDeclined means the user said no.
func RemoveFirewallRule() error {
	return runElevated(`Remove-NetFirewallRule -Name ` + psQuote(ruleName) + ` -ErrorAction SilentlyContinue`)
}

// RemoveRules removes the firewall rules with the given IDs (Rule.ID).
// Windows asks the user for permission first; ErrDeclined means the user
// said no.
func RemoveRules(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return runElevated(removeRulesScript(ids))
}

func removeRulesScript(ids []string) string {
	lines := make([]string, len(ids))
	for i, id := range ids {
		lines[i] = `Remove-NetFirewallRule -Name ` + psQuote(id) + ` -ErrorAction SilentlyContinue`
	}
	return strings.Join(lines, "\n")
}

// OpenNetworkSettings opens Windows' network settings, where the user can
// mark a network as private.
func OpenNetworkSettings() error {
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr("ms-settings:network-status"),
		nil, nil, windows.SW_SHOWNORMAL)
}

// psQuote writes a text as a PowerShell string that is taken literally.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// encodeScript prepares a script for -EncodedCommand (Base64 of UTF-16LE),
// which spares any quoting on the command line.
func encodeScript(script string) string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		raw[2*i] = byte(u)
		raw[2*i+1] = byte(u >> 8)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// powerShell returns the path of Windows' own PowerShell, taken from the
// system folder rather than PATH, which other programs can change.
func powerShell() (string, error) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("find the Windows system folder: %w", err)
	}
	return filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), nil
}

// powerShellArgs are the options of every PowerShell CryptBridge starts.
func powerShellArgs(script string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodeScript(script)}
}

// runPowerShell runs a script without a window and returns what it prints.
func runPowerShell(ctx context.Context, script string) ([]byte, error) {
	exe, err := powerShell()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, exe, powerShellArgs(script)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return bytes.TrimPrefix(bytes.TrimSpace(output), []byte("\xef\xbb\xbf")), nil
}

// runElevated runs a script as administrator after Windows asked the user.
// The script's error message comes back through a file, because an elevated
// process cannot hand its output to an ordinary one.
func runElevated(script string) error {
	errorFile, err := os.CreateTemp("", "cryptbridge-firewall-*.txt")
	if err != nil {
		return fmt.Errorf("prepare the firewall change: %w", err)
	}
	errorPath := errorFile.Name()
	errorFile.Close()
	defer os.Remove(errorPath)

	wrapped := `$ErrorActionPreference = 'Stop'
try {
` + script + `
} catch {
  [IO.File]::WriteAllText(` + psQuote(errorPath) + `, $_.Exception.Message)
  exit 1
}
exit 0`
	code, err := startElevated(wrapped, elevatedTimeout)
	if err != nil {
		return err
	}
	if code == 0 {
		return nil
	}
	message, _ := os.ReadFile(errorPath)
	if text := strings.TrimSpace(string(message)); text != "" {
		return fmt.Errorf("change the Windows firewall: %s", text)
	}
	return fmt.Errorf("change the Windows firewall: PowerShell ended with code %d", code)
}
