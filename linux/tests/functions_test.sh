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
# The conf must contain the remotes cbcloud and cbvault.
#
# Optional check against a block of the Windows program (written by
# TestWriteSampleForOpenSSL in windows/internal/transfer):
#   CB_TEST_TRANSFER_SAMPLE=/folder/with/sample.cbtransfer/and/sample.code \
#   bash linux/tests/functions_test.sh

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

# --- check_web_ports: another web server on 80/443 is named, Caddy is not ---
# Without this check, nginx on port 80 stopped the setup at Caddy, before the
# command and the desktop icons were set up (seen on the test server).
ss() { # stand-in for "ss -Htlnp": what listens, by $listening
	[[ "$listening" == none ]] || cat <<'EOF'
LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:(("nginx",pid=11,fd=5),("nginx",pid=12,fd=5))
LISTEN 0 511 [::]:80 [::]:* users:(("nginx",pid=11,fd=6))
LISTEN 0 511 0.0.0.0:443 0.0.0.0:* users:(("nginx",pid=11,fd=7))
EOF
	[[ "$listening" == all ]] && echo 'LISTEN 0 5 0.0.0.0:80 0.0.0.0:* users:(("python3",pid=30,fd=3))'
	echo 'LISTEN 0 4096 *:443 *:* users:(("caddy",pid=20,fd=4))'
}
ps() { # stand-in for "ps -o comm=|unit= -p PID"
	case "$2:$4" in
	comm=:11 | comm=:12) echo nginx ;;
	unit=:11 | unit=:12) echo nginx.service ;;
	comm=:20) echo caddy ;;
	unit=:20) echo caddy.service ;;
	comm=:30) echo python3 ;;
	unit=:30) echo session-3.scope ;;
	esac
}
systemctl() { # stand-in: stopping nginx frees its ports
	systemctl_calls+="$*;"
	[[ "$*" == "disable --now nginx.service" && "$listening" == nginx ]] && listening=none
	return 0
}
confirm() { asked+="$1|"; [[ "$answer" == yes ]]; }

listening=all
check "web_port_users: each program once with its ports, Caddy left out" \
	$'nginx nginx.service 80 and 443\npython3 session-3.scope 80' "$(web_port_users | sort)"
for case in "nginx no 0 asked" "nginx yes 1 asked" "none yes 1 not-asked" "all yes 0 asked"; do
	read -r users answer want_https want_asked <<<"$case"
	listening="$users" HTTPS_POSSIBLE=1 asked="" systemctl_calls=""
	check_web_ports >/dev/null 2>&1 # may change $listening, as stopping would
	name="ports used by $users, answer $answer"
	check "$name: HTTPS possible" "$want_https" "$HTTPS_POSSIBLE"
	[[ -n "$asked" ]] && r=asked || r=not-asked
	check "$name: asked before stopping" "$want_asked" "$r"
	[[ "$asked" == *python3* ]] && check "$name: no offer to stop a program without a service" none "$asked"
	want=kept
	[[ "$answer" == yes && "$users" != none ]] && want=stopped
	[[ "$systemctl_calls" == *"disable --now nginx.service"* ]] && r=stopped || r=kept
	check "$name: nginx stopped only when the user said yes" "$want" "$r"
done
unset -f ss ps systemctl confirm
HTTPS_POSSIBLE=1

# --- switch_streaming: Caddy off frees 80/443 for a website, and stays off ---
# Settings of CryptBridge 0.8.2 do not mention streaming: it must count as on.
old_root="$CB_TEST_ROOT/old-0.8.2"
mkdir -p "$old_root/etc/cryptbridge" # CONF_DIR of a setup.sh with CB_TEST_ROOT=$old_root
grep -v '^CB_STREAMING=' "$SETTINGS_FILE" >"$old_root/etc/cryptbridge/settings"
r="$(CB_TEST_ROOT="$old_root" bash -c '. "$1"; load_settings; echo "$CB_STREAMING"' _ "$here/../setup.sh")"
check "streaming: settings without it count as on" on "$r"

calls_file="$CB_TEST_ROOT/systemctl.calls"
require_root() { :; }
systemctl() { # stand-in: records the calls (also from subshells), all services run
	printf '%s;' "$*" >>"$calls_file"
	[[ "$1" == "is-active" ]] && echo active
	return 0
}
web_port_users() { [[ "$port_users" == none ]] || echo "$port_users"; }
calls() { cat "$calls_file" 2>/dev/null; : >"$calls_file"; }
saved_streaming() { (CB_STREAMING="" && load_settings && echo "$CB_STREAMING"); }
mkdir -p "$(dirname "$CADDYFILE")"
port_users=none

calls >/dev/null
switch_streaming off >/dev/null
check "streaming off: Caddy stopped and kept off after a reboot" "disable --now caddy;" "$(calls)"
check "streaming off: saved" off "$(saved_streaming)"
check "streaming off: other settings kept" host.example "$(load_settings && echo "$CB_ADDRESS")"

out="$(show_status 2>&1)"
[[ "$out" == *"caddy: switched off"* ]] && r=shown || r="not shown: $out"
check "streaming off: status says switched off, no warning" shown "$r"
[[ "$(calls)" == *"is-active caddy"* ]] && r=asked || r=not-asked
check "streaming off: status does not ask Caddy" not-asked "$r"

restart_services >/dev/null
check "streaming off: restart leaves Caddy off" "restart rclone-mount.service rclone-webdav.service;" "$(calls)"

out="$(check_web_ports 2>&1)"
[[ "$out" == *"switched off"* && "$HTTPS_POSSIBLE" == 1 ]] && r=skipped || r="checked: $out"
check "streaming off: setup leaves ports 80 and 443 alone" skipped "$r"

printf 'not ours\n' >"$CADDYFILE"
out="$( (switch_streaming on) 2>&1)"; status=$?
check "streaming on without the setup's Caddyfile: refused" "1 " "$status $(calls)"
check "streaming on without the setup's Caddyfile: still off" off "$(saved_streaming)"

printf '%s\n' "$MARKER" >"$CADDYFILE"
port_users="nginx nginx.service 80 and 443"
out="$( (switch_streaming on) 2>&1)"; status=$?
check "streaming on, nginx on the ports: refused, Caddy not started" "1 " "$status $(calls)"
[[ "$out" == *"Ports 80 and 443 are used by nginx"* && "$out" == *"sudo systemctl stop nginx.service"* ]] && r=explained || r="unclear: $out"
check "streaming on, nginx on the ports: names nginx and how to stop it" explained "$r"
check "streaming on, nginx on the ports: still off" off "$(saved_streaming)"

port_users=none
switch_streaming on >/dev/null
check "streaming on, ports free: Caddy started and kept on" "enable --now caddy;" "$(calls)"
check "streaming on: saved" on "$(saved_streaming)"
restart_services >/dev/null
check "streaming on: restart includes Caddy" "restart rclone-mount.service rclone-webdav.service caddy.service;" "$(calls)"
unset -f require_root systemctl web_port_users calls saved_streaming

# --- choose_address: an IP address is explained and asked again ------------
check "157.173.96.238 is an IP address" yes "$(is_ipv4_address 157.173.96.238 && echo yes || echo no)"
check "myvault.duckdns.org is no IP address" no "$(is_ipv4_address myvault.duckdns.org && echo yes || echo no)"
check "1.2.3 is no IP address" no "$(is_ipv4_address 1.2.3 && echo yes || echo no)"
ask() { printf -v "$1" '%s' "${typed[0]}"; typed=("${typed[@]:1}"); }
confirm() { [[ "$answer" == yes ]]; }
public_ipv4() { echo 157.173.96.238; }
public_dns_ipv4() { echo 157.173.96.238; }
typed=(157.173.96.238 myvault.duckdns.org) answer=no ADDRESS_IS_IP=0 CB_ADDRESS=""
choose_address >/dev/null 2>&1
check "IP refused: the name typed next is taken" "myvault.duckdns.org 0" "$CB_ADDRESS $ADDRESS_IS_IP"
typed=(157.173.96.238) answer=yes ADDRESS_IS_IP=0 CB_ADDRESS=""
choose_address >/dev/null 2>&1
check "IP kept when the user insists" "157.173.96.238 1" "$CB_ADDRESS $ADDRESS_IS_IP"
unset -f ask confirm public_ipv4 public_dns_ipv4
ADDRESS_IS_IP=0 CB_ADDRESS=""

# --- service_title -----------------------------------------------------------
check "title of drive" "Google Drive" "$(CB_SERVICE=drive service_title)"
check "title when no service is known" "the cloud" "$(CB_SERVICE="" service_title)"
check "title of a service the setup does not know" "the cloud" "$(CB_SERVICE=s3 service_title)"

# --- rclone configuration: written, read back, moved from CryptBridge 0.7 ---
as_user() { "$@"; }                       # the tests run as one user
prepare_rclone_dir() { mkdir -p "$RCLONE_DIR"; } # no root for chown
obscure() { printf 'obscured(%s)' "$1"; }
CB_SERVICE="onedrive" CB_VAULT_PATH="/Films/" VAULT_ENCRYPTION="obfuscate" VAULT_DIRNAMES="false"
CLOUD_SETTINGS=$'drive_id = abc\ndrive_type = personal\ntoken = {"access_token":"x = y"}'
VAULT_PASSWORD="pw" VAULT_SALT=""
write_rclone_conf
check "conf: cloud remote has the service" "onedrive" "$(conf_value "$CLOUD_REMOTE" type)"
check "conf: settings are written" "abc" "$(conf_value "$CLOUD_REMOTE" drive_id)"
check "conf: a value with ' = ' is read back whole" '{"access_token":"x = y"}' "$(conf_value "$CLOUD_REMOTE" token)"
check "conf: vault remote sits on the cloud remote" "cbcloud:Films" "$(conf_value "$VAULT_REMOTE" remote)"
check "conf: no salt, no password2" "" "$(conf_value "$VAULT_REMOTE" password2)"
if ! command -v cygpath >/dev/null; then # file modes mean nothing in Git Bash
	check "conf: only its user can read it" 600 "$(stat -c %a "$RCLONE_CONF")"
fi
CB_SERVICE="" CB_VAULT_PATH="" VAULT_ENCRYPTION="" VAULT_DIRNAMES=""
read_kept_keys
check "kept keys are read back" "onedrive|Films|obfuscate|false" \
	"$CB_SERVICE|$CB_VAULT_PATH|$VAULT_ENCRYPTION|$VAULT_DIRNAMES"

rm -f "$RCLONE_CONF"
printf '%s\n' "$MARKER" "[cbpcloud]" "type = pcloud" "hostname = eapi.pcloud.com" "token = OLD" "" \
	"[cbvault]" "type = crypt" "remote = cbpcloud:Old Vault" "filename_encryption = standard" >"$OLD_RCLONE_CONF"
copy_old_rclone_conf >/dev/null
check "0.7 conf: cloud remote renamed" "pcloud" "$(conf_value "$CLOUD_REMOTE" type)"
check "0.7 conf: token kept" "OLD" "$(conf_value "$CLOUD_REMOTE" token)"
check "0.7 conf: vault points to the renamed remote" "cbcloud:Old Vault" "$(conf_value "$VAULT_REMOTE" remote)"
[[ -f "$OLD_RCLONE_CONF" ]] && r=kept || r=gone
check "0.7 conf stays until the services use the new one" kept "$r"
echo "# changed" >>"$RCLONE_CONF"
copy_old_rclone_conf >/dev/null
check "a second run keeps the new conf" "# changed" "$(tail -n 1 "$RCLONE_CONF")"
remove_old_rclone_conf
[[ -e "$OLD_RCLONE_CONF" ]] && r=kept || r=gone
check "0.7 conf removed at the end" gone "$r"
rm -f "$RCLONE_CONF"
unset -f as_user prepare_rclone_dir obscure

# --- transfer blocks: both formats, and nothing that could change the conf --
if command -v jq >/dev/null; then
	vault_json='"vault":{"path":"Films","filenameEncryption":"standard","directoryNameEncryption":true,"filenameEncoding":"base32","suffix":".bin","password":"pw","password2":"salt"},"fingerprint":"ABCD-1234"'
	# block2 CLOUD — the content of a format-2 block with the sign-in CLOUD
	block2() { printf '{"format":"cryptbridge-transfer-2","cloud":%s,%s}' "$1" "$vault_json"; }
	# taken JSON — runs take_transfer; prints "ok service|settings" or why it stopped
	taken() {
		local out
		if out="$( (take_transfer "$1" && printf '%s|%s' "$CB_SERVICE" "$CLOUD_SETTINGS") 2>&1)"; then
			printf 'ok %s' "$out"
		else
			printf 'stopped %s' "$out"
		fi
	}
	# refused CLOUD — prints "refused" if take_transfer stops on the sign-in CLOUD
	refused() {
		local r
		r="$(taken "$(block2 "$1")")"
		[[ "$r" == "stopped "*"no valid cloud sign-in"* ]] && r=refused
		printf '%s' "$r"
	}

	r="$(taken "$(block2 '{"service":"drive","settings":{"client_id":"id.apps.googleusercontent.com","token":"{\"access_token\":\"T\"}"}}')")"
	check "format 2: Google Drive sign-in taken" \
		$'ok drive|client_id = id.apps.googleusercontent.com\ntoken = {"access_token":"T"}' "$r"
	r="$( (take_transfer "$(block2 '{"service":"dropbox","settings":{"token":"T"}}')" &&
		printf '%s|%s|%s|%s|%s' "$CB_VAULT_PATH" "$VAULT_DIRNAMES" "$VAULT_PASSWORD" "$VAULT_SALT" "$EXPECTED_FINGERPRINT") 2>&1)"
	check "format 2: vault keys taken" "Films|true|pw|salt|ABCD-1234" "$r"
	r="$(taken '{"format":"cryptbridge-transfer-1","pcloud":{"hostname":"eapi.pcloud.com","token":"{\"access_token\":\"P\"}"},'"$vault_json"'}')"
	check "format 1 (CryptBridge 0.7): pCloud sign-in taken" \
		$'ok pcloud|hostname = eapi.pcloud.com\ntoken = {"access_token":"P"}' "$r"

	r="$(taken '{"format":"cryptbridge-transfer-3"}')"
	[[ "$r" == "stopped "*"newer CryptBridge"* ]] && r=newer
	check "a newer format is named as such" newer "$r"
	r="$(taken 'not JSON: the wrong code gave garbage')"
	[[ "$r" == "stopped "*"Wrong transfer code"* ]] && r=wrong
	check "garbage means a wrong code" wrong "$r"
	r="$(taken "$(block2 '{"service":"s3","settings":{"token":"T"}}')")"
	[[ "$r" == "stopped "*"cannot sign in to 's3'"* ]] && r=unknown
	check "an unknown service is refused by name" unknown "$r"

	check "a line break in a value is refused" refused \
		"$(refused '{"service":"drive","settings":{"token":"T\n[evil]\ntype = local"}}')"
	check "a carriage return in a value is refused" refused \
		"$(refused '{"service":"drive","settings":{"token":"T\r[evil]"}}')"
	check "a line break in a key is refused" refused \
		"$(refused '{"service":"drive","settings":{"token":"T","a\n[evil]":"x"}}')"
	check "a key with ' = ' is refused" refused "$(refused '{"service":"drive","settings":{"token":"T","a = b":"x"}}')"
	check "a second type is refused" refused "$(refused '{"service":"drive","settings":{"token":"T","type":"local"}}')"
	check "a value that is no text is refused" refused "$(refused '{"service":"drive","settings":{"token":"T","n":1}}')"
	check "a sign-in without token is refused" refused "$(refused '{"service":"drive","settings":{"client_id":"x"}}')"
	check "a service name with odd characters is refused" refused "$(refused '{"service":"drive]","settings":{"token":"T"}}')"
	unset -f block2 taken refused

	check "a copied code loses case, dashes and invisible characters" K7Q2MABCDEFGHJKLMNPQRSTUV \
		"$(normalise_code $'\xc2\xa0k7q2m-ABCDE\xe2\x80\x8b fghjk\tLMNPQ-rstuv\r')"

	# A block of the Windows program, opened with the real openssl.
	if [[ -n "${CB_TEST_TRANSFER_SAMPLE:-}" ]]; then
		body="$(transfer_body "$(<"$CB_TEST_TRANSFER_SAMPLE/sample.cbtransfer")")"
		code="$(<"$CB_TEST_TRANSFER_SAMPLE/sample.code")"
		r="$( (take_transfer "$(open_transfer_block "$body" "$(tr 'A-Z' 'a-z' <<<"$code")")" &&
			printf '%s|%s|%s' "$CB_SERVICE" "$VAULT_PASSWORD" "$CB_VAULT_PATH") 2>&1)"
		check "Windows block: opened with the code in lower case" 'drive|pässwörd with spaces & "quotes"|CryptBridge' "$r"
		r="$(open_transfer_block "$body" $'\xc2\xa0'"$code"$' \r' | jq -r '.vault.path')"
		check "Windows block: opened with a copied code (invisible characters)" CryptBridge "$r"
		r="$( (take_transfer "$(open_transfer_block "$body" "$code")" && printf '%s' "$CLOUD_SETTINGS") 2>&1)"
		check "Windows block: every setting of the account arrives" $'client_id = 123-abc.apps.googleusercontent.com\nclient_secret = GOCSPX-secret\nscope = drive\nteam_drive = \ntoken = {"access_token":"T","token_type":"Bearer","refresh_token":"R","expiry":"2026-10-10T23:00:00Z"}' "$r"
		# openssl takes a wrong code in about 1 of 256 cases and returns
		# garbage; take_transfer must then stop as well.
		r="$( (take_transfer "$(open_transfer_block "$body" "AAAAA-BBBBB-CCCCC-DDDDD-EEEEE")") 2>&1)"
		[[ "$r" == *"Wrong transfer code"* ]] && r=wrong
		check "Windows block: a wrong code is refused" wrong "$r"
		r=opened
		open_transfer_block "$body" "AAAAA-BBBBB-CCCCC-DDDDD-EEEEE" >/dev/null || r=failed
		check "Windows block: a wrong code fails without ending the setup" failed "$r"
	fi
else
	printf 'skip  transfer blocks (jq not installed)\n'
fi

# --- verify_keys with vaults whose names prove nothing (needs rclone) -------
# A folder stands in for the cloud, so no account is needed.
rclone_bin="${CB_TEST_RCLONE:-$(command -v rclone || true)}"
if [[ -n "$rclone_bin" ]]; then
	cloud_dir="$CB_TEST_ROOT/cloud"
	mkdir -p "$cloud_dir"
	# rclone for Windows (when the tests run in Git Bash) needs a Windows path.
	if command -v cygpath >/dev/null; then cloud_dir="$(cygpath -m "$cloud_dir")"; fi

	# local_conf FILE FOLDER MODE PASSWORD — a conf whose "cloud" is the folder above
	local_conf() {
		{
			printf '[%s]\ntype = alias\nremote = %s\n\n' "$CLOUD_REMOTE" "$cloud_dir"
			printf '[%s]\ntype = crypt\nremote = %s:%s\n' "$VAULT_REMOTE" "$CLOUD_REMOTE" "$2"
			printf 'filename_encryption = %s\ndirectory_name_encryption = true\n' "$3"
			printf 'filename_encoding = base32\nsuffix = .bin\npassword = %s\n' "$(printf '%s' "$4" | "$rclone_bin" obscure -)"
		} >"$1"
	}
	# key_check FOLDER MODE PASSWORD — runs verify_keys, prints "status output"
	key_check() {
		CB_VAULT_PATH="$1" VAULT_ENCRYPTION="$2" VAULT_DIRNAMES="true"
		local_conf "$test_conf" "$1" "$2" "$3"
		local out status
		out="$( (verify_keys) 2>&1)"; status=$?
		printf '%s %s' "$status" "$out"
	}
	rc() { "$rclone_bin" --config "$test_conf" "$@"; }
	confirm() { return 1; } # answers "no" to "Continue anyway?"
	test_conf="$CB_TEST_ROOT/local.conf"

	for mode in obfuscate off; do
		local_conf "$test_conf" "Vault-$mode" "$mode" "right password"
		printf 'This file lets CryptBridge verify the keys of this vault.\r\n' | rc rcat "$VAULT_REMOTE:$CHECK_FILE"
		printf 'some notes' | rc rcat "$VAULT_REMOTE:Notes 2026.txt"
		rc mkdir "$VAULT_REMOTE:Photos"

		r="$(key_check "Vault-$mode" "$mode" "right password")"
		[[ "$r" == "0 "*"a file was decrypted"* ]] && r=accepted
		check "$mode: verify_keys accepts the right keys by content" accepted "$r"
		r="$(key_check "Vault-$mode" "$mode" "wrong password")"
		[[ "$r" == "1 "*"do NOT open"*"did not decrypt"* ]] && r=refused
		check "$mode: verify_keys refuses a wrong password" refused "$r"
		r="$(key_check "Vault-$mode" standard "right password")"
		[[ "$r" == "1 "*"do NOT open"* ]] && r=refused
		check "$mode: verify_keys refuses the right password with standard names" refused "$r"
	done
	r="$(key_check "Vault-obfuscate" off "right password")"
	[[ "$r" == "1 "*"fit its name settings"* ]] && r=refused
	check "obfuscate: verify_keys refuses the right password with plain names" refused "$r"

	# Only an empty file: nothing can prove the keys, the user is asked.
	local_conf "$test_conf" "Vault-empty" off "right password"
	: | rc rcat "$VAULT_REMOTE:empty.txt"
	r="$(key_check "Vault-empty" off "right password")"
	[[ "$r" == "1 "*"cannot be checked"*"Nothing changed"* ]] && r=asked
	check "off: verify_keys asks when nothing can prove the keys" asked "$r"
	unset -f rc confirm local_conf key_check
fi

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
