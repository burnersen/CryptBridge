#!/usr/bin/env bash
# CryptBridge (https://github.com/burnersen/CryptBridge)
# Copyright (C) 2026 burnersen
# SPDX-License-Identifier: GPL-3.0-only
#
# Tests the functions of setup.sh that need no root rights.
#
#   bash linux/tests/functions_test.sh
#
# Optional live part (talks to a real pCloud test vault):
#   CB_TEST_RCLONE=/path/to/rclone CB_TEST_CONF=/path/to/rclone.conf \
#   CB_TEST_VAULT_PATH=CryptBridge-Test/vault CB_TEST_FINGERPRINT=XXXX-XXXX \
#   bash linux/tests/functions_test.sh
# The conf must contain the remotes cbpcloud and cbvault.

set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
CB_TEST_ROOT="$(mktemp -d)"
export CB_TEST_ROOT
trap 'chmod -R u+w "$CB_TEST_ROOT"; rm -rf "$CB_TEST_ROOT"' EXIT

# shellcheck source=../setup.sh
. "$here/../setup.sh"
set +e        # the tests check results themselves
trap - ERR    # setup.sh's error trap would end the test run
trap 'chmod -R u+w "$CB_TEST_ROOT"; rm -rf "$CB_TEST_ROOT"' EXIT

failures=0
check() { # check "name" expected actual
	if [[ "$2" == "$3" ]]; then
		printf 'ok    %s\n' "$1"
	else
		printf 'FAIL  %s\n      expected: %q\n      actual:   %q\n' "$1" "$2" "$3"
		failures=$((failures + 1))
	fi
}

# --- normalise_path: must match vault.Config.NormalisedPath in Go ---------
check "normalise plain" "Vault" "$(normalise_path "Vault")"
check "normalise slashes and spaces" "Videos/Vault" "$(normalise_path ' /Videos\Vault/ ')"
check "normalise only slashes" "" "$(normalise_path "///")"

# --- version_ge ------------------------------------------------------------
version_ge 1.75.1 1.75.0 && r=yes || r=no; check "1.75.1 >= 1.75.0" yes "$r"
version_ge 1.75.0 1.75.0 && r=yes || r=no; check "1.75.0 >= 1.75.0" yes "$r"
version_ge 1.9.0 1.10.0 && r=yes || r=no; check "1.9.0 >= 1.10.0 is false" no "$r"
version_ge 2.0.0 1.99.9 && r=yes || r=no; check "2.0.0 >= 1.99.9" yes "$r"

# --- save_settings / load_settings: odd values must survive and never run ---
mkdir -p "$CONF_DIR"
CB_USER="tester" CB_MOUNT_DIR="/home/tester/Crypt Bridge" CB_ADDRESS="host.example"
CB_VAULT_PATH='Films "4K" $(touch '"$CB_TEST_ROOT"'/pwned) `id` ; echo x'
CB_WEBDAV_USER="infuse" CB_CACHE_MAX="20G" CB_CACHE_MIN_FREE="5G" CB_FINGERPRINT="ABCD-1234"
saved_path="$CB_VAULT_PATH"
save_settings
CB_VAULT_PATH="" CB_MOUNT_DIR=""
load_settings
check "settings: odd vault path survives" "$saved_path" "$CB_VAULT_PATH"
check "settings: path with space survives" "/home/tester/Crypt Bridge" "$CB_MOUNT_DIR"
[[ -e "$CB_TEST_ROOT/pwned" ]] && r=executed || r=safe
check "settings: value is never executed" safe "$r"

# --- admin_ports: the SSH port of this connection must be found ------------
if [[ -n "${SSH_CONNECTION:-}" ]]; then
	ssh_port="$(awk '{print $4}' <<<"$SSH_CONNECTION")"
	CB_SSH_PORT=""
	ports="$(admin_ports | tr '\n' ' ')"
	[[ " $ports" == *" $ssh_port "* ]] && r=found || r="missing (found: $ports)"
	check "admin_ports finds SSH port $ssh_port without being told" found "$r"
fi

# --- setup_firewall: ports 80 and 443 are allowed even if ufw stays off ----
# ufw keeps rules while it is off; without them, switching ufw on later
# (by hand) shut out the certificate and the video players.
ufw() { # stand-in: records the calls, reports $ufw_state
	ufw_calls+="$*;"
	[[ "$1" == "status" ]] && printf 'Status: %s\n' "$ufw_state"
	return 0
}
admin_ports() { echo 22; }
LOG_FILE="$CB_TEST_ROOT/setup.log"
for ufw_state in inactive active; do
	for answer in no yes; do
		ufw_calls="" asked=no
		confirm() { asked=yes; [[ "$answer" == yes ]]; }
		setup_firewall >/dev/null
		name="firewall $ufw_state, answer $answer"
		[[ "$ufw_calls" == *"allow 80/tcp"* && "$ufw_calls" == *"allow 443/tcp"* ]] && r=allowed || r="not allowed: $ufw_calls"
		check "$name: 80 and 443 allowed" allowed "$r"
		want=off
		[[ "$ufw_state" == inactive && "$answer" == yes ]] && want=on
		[[ "$ufw_calls" == *"--force enable"* ]] && r=on || r=off
		check "$name: switched on only when asked and wanted" "$want" "$r"
		[[ "$ufw_state" == active ]] && check "$name: no question when ufw is on" no "$asked"
	done
done
unset -f ufw admin_ports confirm

# --- live part: same check code and key check as the Windows program -------
if [[ -n "${CB_TEST_CONF:-}" ]]; then
	rc() { "$CB_TEST_RCLONE" --config "$CB_TEST_CONF" "$@"; }
	CB_VAULT_PATH="$CB_TEST_VAULT_PATH" VAULT_ENCRYPTION="standard" VAULT_DIRNAMES="true" EXPECTED_FINGERPRINT=""
	compute_fingerprint >/dev/null
	check "check code equals the Windows program" "$CB_TEST_FINGERPRINT" "$CB_FINGERPRINT"

	out="$(verify_keys 2>&1)"; status=$?
	check "verify_keys accepts the right keys" 0 "$status"

	wrong_conf="$CB_TEST_ROOT/wrong.conf"
	sed "s|^password = .*|password = $(printf 'wrong-password' | "$CB_TEST_RCLONE" obscure -)|" "$CB_TEST_CONF" >"$wrong_conf"
	rc() { "$CB_TEST_RCLONE" --config "$wrong_conf" "$@"; }
	out="$( (verify_keys) 2>&1)"; status=$?
	[[ "$out" == *"do NOT open"* ]] && r=refused || r="not refused: $out"
	check "verify_keys refuses a wrong password" refused "$r"
	check "verify_keys exits with an error" 1 "$status"
fi

printf '\n%d failure(s)\n' "$failures"
exit $((failures > 0))
