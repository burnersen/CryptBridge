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

# --- the pinned rclone: checksums, sources, exactly one version -------------
[[ "$(rclone_sha256 amd64)" =~ ^[0-9a-f]{64}$ ]] && r=yes || r=no; check "rclone checksum for amd64" yes "$r"
[[ "$(rclone_sha256 arm64)" =~ ^[0-9a-f]{64}$ ]] && r=yes || r=no; check "rclone checksum for arm64" yes "$r"
rclone_sha256 386 >/dev/null && r=yes || r=no; check "no rclone checksum for 386" no "$r"
check "rclone sources in order" \
	"https://downloads.rclone.org/v$RCLONE_VERSION/z.zip https://github.com/rclone/rclone/releases/download/v$RCLONE_VERSION/z.zip https://github.com/burnersen/CryptBridge/releases/download/v$CRYPTBRIDGE_VERSION/z.zip" \
	"$(rclone_sources z.zip | tr '\n' ' ' | sed 's/ $//')"

# install_rclone with fake tools: the first server fails, the second sends a
# wrong file, the third the right one. Prints the asked URLs and what was
# installed; INSTALLED names the version that "rclone version" reports.
fake_install_rclone() {
	(
		ARCH=amd64
		asked="$CB_TEST_ROOT/rclone.asked" installed="$CB_TEST_ROOT/rclone.installed"
		: >"$asked"
		: >"$installed"
		mkdir -p "$(dirname "$RCLONE_BIN")"
		rm -f "$RCLONE_BIN"
		# The #! line counts in Git Bash, where chmod alone makes nothing executable.
		[[ -n "$INSTALLED" ]] && printf '#!/bin/sh\n' >"$RCLONE_BIN" && chmod +x "$RCLONE_BIN"
		rclone_version() { [[ -n "$INSTALLED" ]] && printf '%s' "$INSTALLED"; }
		rclone_sha256() { printf '%s' "$(printf 'right zip' | sha256sum | awk '{print $1}')"; }
		curl() {
			local url="${*: -3:1}" out="${*: -1}"
			printf '%s\n' "${url%%/v*}" >>"$asked"
			case "$url" in
			*downloads.rclone.org*) [[ "$SERVERS" == all-wrong ]] && printf 'wrong zip' >"$out" || return 22 ;;
			*/rclone/rclone/*) printf 'wrong zip' >"$out" ;;
			*) [[ "$SERVERS" == all-wrong ]] && printf 'wrong zip' >"$out" || printf 'right zip' >"$out" ;;
			esac
		}
		unzip() { mkdir -p "${*: -1}/rclone-v$RCLONE_VERSION-linux-$ARCH" && : >"${*: -1}/rclone-v$RCLONE_VERSION-linux-$ARCH/rclone"; }
		install() { printf '%s\n' "${*: -1}" >>"$installed"; }
		(install_rclone) >/dev/null 2>&1 # die ends only this inner shell
		echo "status=$? asked=$(tr '\n' ' ' <"$asked")installed=$(cat "$installed")"
	)
}
check "pinned rclone installed: nothing downloaded" "status=0 asked=installed=" \
	"$(INSTALLED="$RCLONE_VERSION" SERVERS=ok fake_install_rclone)"
check "other rclone: replaced, next server after a failure and a wrong checksum" \
	"status=0 asked=https://downloads.rclone.org https://github.com/rclone/rclone/releases/download https://github.com/burnersen/CryptBridge/releases/download installed=$RCLONE_BIN" \
	"$(INSTALLED=1.75.1 SERVERS=ok fake_install_rclone)"
check "newer rclone: replaced too (exactly one version)" "status=0" \
	"$(INSTALLED=1.76.0 SERVERS=ok fake_install_rclone | cut -d' ' -f1)"
check "no rclone yet: installed" "status=0" \
	"$(INSTALLED='' SERVERS=ok fake_install_rclone | cut -d' ' -f1)"
check "only wrong files: stops, installs nothing" \
	"status=1 asked=https://downloads.rclone.org https://github.com/rclone/rclone/releases/download https://github.com/burnersen/CryptBridge/releases/download installed=" \
	"$(INSTALLED=1.75.1 SERVERS=all-wrong fake_install_rclone)"

# --- save_settings / load_settings: odd values must survive and never run ---
mkdir -p "$CONF_DIR"
CB_USER="tester" CB_ADDRESS="host.example" CB_WEBDAV_USER="infuse" CB_CACHE_MAX="20G" CB_CACHE_MIN_FREE="5G"
CB_MOUNT_DIR='/home/tester/Crypt Bridge "4K" $(touch '"$CB_TEST_ROOT"'/pwned) `id` ; echo x'
saved_dir="$CB_MOUNT_DIR"
save_settings
CB_MOUNT_DIR=""
load_settings
check "settings: odd folder with spaces survives" "$saved_dir" "$CB_MOUNT_DIR"
[[ -e "$CB_TEST_ROOT/pwned" ]] && r=executed || r=safe
check "settings: value is never executed" safe "$r"
grep -qE '^CB_(VAULT_PATH|FINGERPRINT)=' "$SETTINGS_FILE" && r=there || r=gone
check "settings: nothing of a vault in them (each has its own file)" gone "$r"
CB_MOUNT_DIR="$CB_TEST_ROOT/home/tester/Crypt Bridge"
save_settings

# --- vault files, names, services, switching and deleting -------------------
# In a subshell: the stand-ins below must not replace the real functions for
# the tests further down. It returns its number of failures.
(
	failures=0
	LOG_FILE="$CB_TEST_ROOT/setup.log"
	mkdir -p "$CB_MOUNT_DIR" "$UNIT_DIR" "$RCLONE_DIR"

	# Vault files: odd values survive and never run, numbers in order.
	CB_VAULT_ID=1 CB_SERVICE="pcloud" CB_VAULT_PATH="Videos/Filme" CB_FINGERPRINT="ABCD-1234" CB_VAULT_STATE="on"
	CB_VAULT_NAME='Filme "4K" $(touch '"$CB_TEST_ROOT"'/pwned-vault) `id` ; echo x'
	saved_name="$CB_VAULT_NAME"
	save_vault
	CB_VAULT_ID=10 CB_VAULT_NAME="Zehn"
	save_vault
	CB_VAULT_ID=2 CB_VAULT_NAME="Serien" CB_SERVICE="drive" CB_VAULT_PATH="/Serien/" CB_FINGERPRINT="EEEE-0000" CB_VAULT_STATE="off"
	save_vault
	echo "not a vault" >"$VAULTS_DIR/README"
	check "vault numbers in order, other files left out" "1 2 10" "$(vault_ids | paste -sd ' ')"
	check "a new vault gets the next number" 11 "$(next_vault_id)"
	select_vault 1
	check "vault file: odd name survives" "$saved_name" "$CB_VAULT_NAME"
	check "vault file: settings read back" "pcloud|Videos/Filme|ABCD-1234|on|$RCLONE_DIR/1.conf" \
		"$CB_SERVICE|$CB_VAULT_PATH|$CB_FINGERPRINT|$CB_VAULT_STATE|$RCLONE_CONF"
	[[ -e "$CB_TEST_ROOT/pwned-vault" ]] && r=executed || r=safe
	check "vault file: a value is never executed" safe "$r"
	check "vault_name leaves the selected vault alone" "Serien|$saved_name" "$(vault_name 2)|$CB_VAULT_NAME"
	rm -f "$VAULTS_DIR/10" "$VAULTS_DIR/README"
	CB_VAULT_NAME="Filme"
	save_vault

	# Names: folder names that work in the file manager, the players and systemd.
	for name in "Filme" "Filme & Serien" "Kinder (alt)" "Bob's Films" $'K\xc3\xbcche' "v1.2" "$(printf 'x%.0s' {1..60})"; do
		valid_vault_name "$name" && r=valid || r=refused
		check "vault name $(printf '%q' "$name") is valid" valid "$r"
	done
	for name in "" ".hidden" " lead" "trail " "a/b" 'a\b' 'a"b' 'a$b' 'a%b' 'a`b' $'a\tb' $'a\nb' "$(printf 'x%.0s' {1..61})"; do
		valid_vault_name "$name" && r=valid || r=refused
		check "vault name $(printf '%q' "$name") is refused" refused "$r"
	done
	check "a name is taken in any case" yes "$(vault_name_taken SERIEN && echo yes || echo no)"
	check "a vault's own name is no obstacle" no "$(vault_name_taken Serien 2 && echo yes || echo no)"
	check "a free name" no "$(vault_name_taken Dokus && echo yes || echo no)"
	check "suggestion: the name from Windows" "Filme neu" "$(CB_VAULT_NAME="Filme neu" CB_VAULT_PATH="x/y" suggested_vault_name)"
	check "suggestion: else the last part of the cloud folder" "Dokus" "$(CB_VAULT_NAME="" CB_VAULT_PATH="/Videos/Dokus/" suggested_vault_name)"
	check "suggestion: a vault at the top of the cloud" "Vault" "$(CB_VAULT_NAME="" CB_VAULT_PATH="" suggested_vault_name)"

	ask() { # stand-in: answers from $typed in turn ("" = Enter), notes the suggestions
		suggestions+="[${3:-}]"
		local answer="ran-out"
		if ((${#typed[@]} > 0)); then
			answer="${typed[0]}"
			typed=("${typed[@]:1}")
		fi
		printf -v "$1" '%s' "${answer:-${3:-}}"
	}
	findmnt() { [[ "${mounted:-no}" == yes ]]; } # stand-in for the mount table
	mkdir -p "$CB_MOUNT_DIR/Dokus"              # a folder that is no vault
	CB_VAULT_ID=3 suggestions="" typed=("a/b" "serien" "Dokus" "Doku Neu")
	choose_vault_name "Serien" >/dev/null 2>&1
	check "vault name: a wrong, taken or existing name is asked again" "Doku Neu" "$CB_VAULT_NAME"
	check "vault name: a taken suggestion is not offered" "[][][][]" "$suggestions"
	CB_VAULT_ID=3 suggestions="" typed=("")
	choose_vault_name "Filme 2" >/dev/null 2>&1
	check "vault name: Enter takes the suggestion" "Filme 2|[Filme 2]" "$CB_VAULT_NAME|$suggestions"
	rmdir "$CB_MOUNT_DIR/Dokus"

	check "find_vault: the same vault" 2 "$(CB_SERVICE=drive CB_VAULT_PATH='Serien' CB_FINGERPRINT=EEEE-0000 find_vault)"
	check "find_vault: other keys in the same folder" "" "$(CB_SERVICE=drive CB_VAULT_PATH='Serien' CB_FINGERPRINT=FFFF-1111 find_vault)"
	check "find_vault: the same folder in another cloud" "" "$(CB_SERVICE=dropbox CB_VAULT_PATH='Serien' CB_FINGERPRINT=EEEE-0000 find_vault)"

	# Service files: the vault's folder is one quoted argument.
	CB_USER="tester" CB_GROUP="testers"
	select_vault 1
	CB_VAULT_NAME="Filme & Serien" # not saved
	write_vault_unit
	unit="$(<"$UNIT_DIR/rclone-mount-1.service")"
	[[ "$unit" == *"ExecStart=$RCLONE_BIN mount cbvault: \"$CB_MOUNT_DIR/Filme & Serien\" \\"* &&
		"$unit" == *"ExecStop=/usr/bin/fusermount3 -uz \"$CB_MOUNT_DIR/Filme & Serien\""* ]] && r=quoted || r="$unit"
	check "vault unit: the folder is quoted" quoted "$r"
	[[ "$unit" == *"--config $RCLONE_DIR/1.conf "* && "$unit" == *"--cache-dir $CACHE_DIR/1 "* ]] && r=own || r="$unit"
	check "vault unit: keys and cache of its own" own "$r"
	write_webdav_unit
	unit="$(<"$WEBDAV_UNIT")"
	[[ "$unit" == *"serve webdav \"$CB_MOUNT_DIR\""* && "$unit" == *'--config ""'* && "$unit" != *BindsTo* ]] && r=folder || r="$unit"
	check "WebDAV unit: serves the folder of the vaults, bound to no vault" folder "$r"
	select_vault 1

	# Files waiting for their upload, as rclone notes them in the cache.
	meta="$CACHE_DIR/1/vfsMeta/cbvault"
	mkdir -p "$meta/sub"
	printf '{\n\t"Size": 6,\n\t"Dirty": true\n}\n' >"$meta/waiting.mkv"
	printf '{\n\t"Dirty": false\n}\n' >"$meta/uploaded.mkv"
	printf '{\n\t"Dirty": true\n}\n' >"$meta/sub/also waiting.mkv"
	check "waiting uploads are counted" 2 "$(pending_uploads 1)"
	check "a vault without cache has none" 0 "$(pending_uploads 2)"

	# Switching: off unmounts and hides the folder, on brings it back.
	calls_file="$CB_TEST_ROOT/vault.calls"
	systemctl() { # stand-in: records the calls (also from subshells)
		printf '%s;' "$*" >>"$calls_file"
		[[ "$1" == "is-active" ]] && echo "$active"
		return 0
	}
	calls() { cat "$calls_file" 2>/dev/null; : >"$calls_file"; }
	require_root() { :; }
	load_user() { :; }
	as_user() { "$@"; }
	state_of() { (select_vault "$1" && echo "$CB_VAULT_STATE"); }
	mkdir -p "$CB_MOUNT_DIR/Filme"
	active=active mounted=no
	calls >/dev/null
	out="$(switch_vault off filme 2>&1)"
	check "vault off: stopped, and kept off after a reboot" "disable --now rclone-mount-1.service;" "$(calls)"
	[[ -d "$CB_MOUNT_DIR/Filme" ]] && r=shown || r=hidden
	check "vault off: its folder is gone, so players no longer show it" hidden "$r"
	check "vault off: saved" off "$(state_of 1)"
	[[ "$out" == *"2 file(s) still wait"* ]] && r=told || r="not told: $out"
	check "vault off: waiting uploads are named" told "$r"
	active=inactive mounted=yes
	out="$(switch_vault on FILME 2>&1)"
	check "vault on: started, and kept on" "enable rclone-mount-1.service;restart rclone-mount-1.service;" "$(calls)"
	check "vault on: saved" on "$(state_of 1)"
	active=active
	out="$(switch_vault on Filme 2>&1)"
	check "vault on while it runs: nothing restarted" "is-active rclone-mount-1.service;" "$(calls)"
	out="$( (switch_vault off Dokus) 2>&1)"
	status=$?
	[[ $status == 1 && "$out" == *"no vault called 'Dokus'"*"'Filme', 'Serien'"* ]] && r=named || r="$status $out"
	check "an unknown vault: refused, the vaults are named" named "$r"
	calls >/dev/null

	# Deleting: only after a yes, and waiting uploads are named first.
	confirm() { [[ "$answer" == yes ]]; }
	touch "$UNIT_DIR/rclone-mount-1.service" "$RCLONE_DIR/1.conf"
	answer=no
	out="$(delete_vault Filme 2>&1)"
	[[ -f "$VAULTS_DIR/1" && -d "$CACHE_DIR/1" && -f "$RCLONE_DIR/1.conf" ]] && r=kept || r=deleted
	check "delete, answer no: nothing deleted" kept "$r"
	[[ "$out" == *"2 file(s) of this vault are not uploaded yet"* ]] && r=warned || r="not warned: $out"
	check "delete: waiting uploads are named before the question" warned "$r"
	answer=yes mounted=no
	out="$(delete_vault Filme 2>&1)"
	[[ -e "$VAULTS_DIR/1" || -e "$CACHE_DIR/1" || -e "$RCLONE_DIR/1.conf" || -e "$UNIT_DIR/rclone-mount-1.service" ]] && r=left || r=deleted
	check "delete, answer yes: vault file, cache, keys and service gone" deleted "$r"
	check "delete: the other vault stays" 2 "$(vault_ids | paste -sd ' ')"

	# Adding: the keys of a vault on the server renew it, other keys add one.
	get_keys() { CB_SERVICE="drive" CB_VAULT_PATH="Serien" CB_VAULT_NAME="Serien (Windows)"; }
	configure_remotes() { printf 'keys of the block\n' >"$RCLONE_CONF"; }
	verify_keys() { :; }
	compute_fingerprint() { CB_FINGERPRINT="$fingerprint"; }
	printf 'old keys\n' >"$RCLONE_DIR/2.conf"
	fingerprint="EEEE-0000"
	calls >/dev/null
	add_vault >/dev/null 2>&1
	check "add, same vault: renewed, not added" "2|Serien|keys of the block" \
		"$(vault_ids | paste -sd ' ')|$CB_VAULT_NAME|$(<"$RCLONE_DIR/2.conf")"
	check "add, same vault: its mount stops before it takes the new keys" "stop rclone-mount-2.service;" "$(calls)"
	[[ -e "$NEW_RCLONE_CONF" ]] && r=left || r=gone
	check "add: no keys left waiting" gone "$r"
	fingerprint="FFFF-1111" typed=("")
	add_vault >/dev/null 2>&1
	check "add, other keys: a new vault, named as in Windows, on" "2 3|Serien (Windows)|on|keys of the block" \
		"$(vault_ids | paste -sd ' ')|$(
			select_vault 3
			echo "$CB_VAULT_NAME|$CB_VAULT_STATE"
		)|$(<"$RCLONE_DIR/3.conf")"

	# Back to the vaults the tests below expect: 1 Filme (on), 2 Serien (off).
	rm -f "$VAULTS_DIR/3" "$RCLONE_DIR/3.conf"
	CB_VAULT_ID=1 CB_VAULT_NAME="Filme" CB_SERVICE="pcloud" CB_VAULT_PATH="Videos/Filme" CB_FINGERPRINT="ABCD-1234" CB_VAULT_STATE="on"
	save_vault
	exit "$failures"
)
failures=$((failures + $?))

# --- update from CryptBridge 0.8: the one vault becomes vault 1 -------------
# A fresh test root of its own, laid out as CryptBridge 0.8 left it.
r="$(CB_TEST_ROOT="$CB_TEST_ROOT/update-0.8" bash -s "$here/../setup.sh" 2>&1 <<'EOF'
. "$1"
set +e
trap - ERR
systemctl() { printf '%s;' "$*" >>"$CB_TEST_ROOT/calls"; }
start_vault() { printf 'start %s;' "$(vault_unit)" >>"$CB_TEST_ROOT/calls"; }
as_user() { "$@"; }
install() { mkdir -p "${@: -1}"; } # no chown without root
ask() { printf -v "$1" '%s' "${3:-ran-out}"; } # takes the suggestion; never loops for want of one
findmnt() { return 1; }             # nothing mounted
mkdir -p "$CONF_DIR" "$RCLONE_DIR" "$UNIT_DIR" "$CACHE_DIR/vfs/cbvault" "$CACHE_DIR/vfsMeta/cbvault" "$(dirname "$LOG_FILE")"
# The settings file as CryptBridge 0.8.3 writes it.
cat >"$SETTINGS_FILE" <<SETTINGS
CB_USER=$(id -un)
CB_MOUNT_DIR=$CB_TEST_ROOT/home/CryptBridge
CB_VAULT_PATH=Videos/Old\ Films
CB_ADDRESS=host.example
CB_WEBDAV_USER=infuse
CB_CACHE_MAX=20G
CB_CACHE_MIN_FREE=5G
CB_FINGERPRINT=1234-ABCD
CB_STREAMING=on
SETTINGS
load_settings
CB_GROUP="testers"
mkdir -p "$CB_MOUNT_DIR"
single_conf() {
	printf '[cbcloud]\ntype = pcloud\ntoken = T\n\n[cbvault]\ntype = crypt\nremote = cbcloud:Videos/Old Films\n' >"$SINGLE_RCLONE_CONF"
	printf 'filename_encryption = standard\ndirectory_name_encryption = true\n' >>"$SINGLE_RCLONE_CONF"
	printf 'old unit\n' >"$OLD_MOUNT_UNIT"
}
single_conf
printf 'film\n' >"$CACHE_DIR/vfs/cbvault/film.mkv"
printf '{\n\t"Dirty": true\n}\n' >"$CACHE_DIR/vfsMeta/cbvault/film.mkv"
update_single_vault >/dev/null 2>&1
echo "ids=$(vault_ids | paste -sd ' ')"
select_vault 1
echo "vault=$CB_VAULT_NAME|$CB_SERVICE|$CB_VAULT_PATH|$CB_FINGERPRINT|$CB_VAULT_STATE"
echo "conf=$(conf_value cbvault remote)"
[[ -e "$SINGLE_RCLONE_CONF" ]] && echo "single=left" || echo "single=gone"
echo "cache=$(cat "$CACHE_DIR/1/vfs/cbvault/film.mkv")|$(pending_uploads 1)"
[[ -e "$OLD_MOUNT_UNIT" ]] && echo "oldunit=left" || echo "oldunit=gone"
grep -qF "mount cbvault: \"$CB_MOUNT_DIR/Old Films\"" "$UNIT_DIR/rclone-mount-1.service" && echo "unit=ok" || echo "unit=wrong"
grep -qE '^CB_(VAULT_PATH|FINGERPRINT)=' "$SETTINGS_FILE" && echo "settings=old" || echo "settings=new"
echo "calls=$(cat "$CB_TEST_ROOT/calls")"
single_conf # as if the setup had been interrupted before the end
update_single_vault >/dev/null 2>&1
echo "rerun=$(vault_ids | paste -sd ' ')"
EOF
)"
line() { grep "^$1=" <<<"$r" | cut -d= -f2-; }
check "update from 0.8: one vault, number 1" 1 "$(line ids)"
check "update from 0.8: named after its cloud folder, settings moved" "Old Films|pcloud|Videos/Old Films|1234-ABCD|on" "$(line vault)"
check "update from 0.8: the keys move to the vault's own file" "cbcloud:Videos/Old Films" "$(line conf)"
check "update from 0.8: the old configuration goes" gone "$(line single)"
check "update from 0.8: the cache moves, with the file waiting for upload" "film|1" "$(line cache)"
check "update from 0.8: the old service goes, the new one has the folder" "gone ok" "$(line oldunit) $(line unit)"
check "update from 0.8: the settings keep nothing of the vault" new "$(line settings)"
check "update from 0.8: stopped first, then the vault and WebDAV started" \
	"disable --now rclone-webdav.service rclone-mount.service;daemon-reload;start rclone-mount-1.service;enable --now rclone-webdav.service;" \
	"$(line calls)"
check "update from 0.8, run again after an interruption: still one vault" 1 "$(line rerun)"
unset -f line

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
systemctl() { # stand-in: nginx starts with the server; stopping it frees its ports
	systemctl_calls+="$*;"
	case "$*" in
	"is-enabled nginx.service") echo enabled ;;
	"show nginx.service -p LoadState --value") echo loaded ;;
	"disable --now nginx.service") [[ "$listening" == nginx ]] && listening=none ;;
	esac
	return 0
}
confirm() { asked+="$1 [$2]|"; [[ "$answer" == yes ]]; }

listening=all
check "web_port_users: each program once with its ports, Caddy left out" \
	$'nginx nginx.service 80 and 443\npython3 session-3.scope 80' "$(web_port_users | sort)"
for case in "nginx no 0 asked" "nginx yes 1 asked" "none yes 1 not-asked" "all yes 0 asked"; do
	read -r users answer want_https want_asked <<<"$case"
	listening="$users" HTTPS_POSSIBLE=1 asked="" systemctl_calls=""
	rm -f "$PAUSED_WEB_SERVERS"
	check_web_ports >/dev/null 2>&1 # may change $listening, as stopping would
	name="ports used by $users, answer $answer"
	check "$name: HTTPS possible" "$want_https" "$HTTPS_POSSIBLE"
	[[ -n "$asked" ]] && r=asked || r=not-asked
	check "$name: asked before pausing" "$want_asked" "$r"
	[[ "$asked" == *python3* ]] && check "$name: no offer to pause a program without a service" none "$asked"
	[[ -n "$asked" ]] && check "$name: asks to pause for streaming, Enter = yes" \
		"Pause nginx while streaming is on? Switching streaming off offers to start it again. [y]|" "$asked"
	want=kept
	[[ "$answer" == yes && "$users" != none ]] && want=paused
	[[ "$systemctl_calls" == *"disable --now nginx.service"* ]] && r=paused || r=kept
	check "$name: nginx paused only when the user said yes" "$want" "$r"
	note="$(cat "$PAUSED_WEB_SERVERS" 2>/dev/null)"
	if [[ "$users" == all ]]; then
		# python3 keeps port 80, so pausing nginx was no use: it comes back.
		[[ "$systemctl_calls" == *"enable --now nginx.service"* ]] && r=back || r=still-paused
		check "$name: nginx started again at once, as it was" back "$r"
		check "$name: nothing left noted" "" "$note"
	elif [[ "$want" == paused ]]; then
		check "$name: noted as paused, with its start at boot" "nginx.service enabled" "$note"
	fi
done
rm -f "$PAUSED_WEB_SERVERS"
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
	case "$*" in
	is-active*) echo active ;;
	"is-enabled nginx.service") echo "$nginx_at_boot" ;;
	"show nginx.service -p LoadState --value") echo "$nginx_installed" ;;
	"disable --now nginx.service" | "stop nginx.service") # frees the ports it held
		port_users="$(grep -v '^nginx ' <<<"$port_users" || true)" ;;
	"enable --now caddy") [[ "$caddy_fails" == no ]] || return 1 ;;
	esac
	return 0
}
web_port_users() { [[ "$port_users" == none || -z "$port_users" ]] || echo "$port_users"; }
confirm() { asked+="$1 [$2]|"; [[ "$answer" == yes ]]; }
paused() { cat "$PAUSED_WEB_SERVERS" 2>/dev/null; }
nginx_at_boot=enabled nginx_installed=loaded caddy_fails=no answer=yes
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
check "streaming off: restart leaves Caddy off (and vault 2, which is off)" "restart rclone-mount-1.service rclone-webdav.service;" "$(calls)"

out="$(check_web_ports 2>&1)"
[[ "$out" == *"switched off"* && "$HTTPS_POSSIBLE" == 1 ]] && r=skipped || r="checked: $out"
check "streaming off: setup leaves ports 80 and 443 alone" skipped "$r"

printf 'not ours\n' >"$CADDYFILE"
out="$( (switch_streaming on) 2>&1)"; status=$?
check "streaming on without the setup's Caddyfile: refused" "1 " "$status $(calls)"
check "streaming on without the setup's Caddyfile: still off" off "$(saved_streaming)"

# A web server on the ports (nginx for a test website) is paused for
# streaming and started again, as it was, when streaming is switched off.
# The switch runs in a subshell, as "die" ends it; so the questions asked
# go into a file, like the systemctl calls.
asked_file="$CB_TEST_ROOT/confirm.asked"
confirm() { printf '%s [%s]|' "$1" "$2" >>"$asked_file"; [[ "$answer" == yes ]]; }
asked() { cat "$asked_file" 2>/dev/null; : >"$asked_file"; }
try_switch() { out="$( (switch_streaming "$1") 2>&1)"; status=$?; }
nginx="nginx nginx.service 80 and 443"
pause_question="Pause nginx while streaming is on? Switching streaming off offers to start it again. [y]|"
printf '%s\n' "$MARKER" >"$CADDYFILE"

answer=no port_users="$nginx"
try_switch on
check "streaming on, nginx kept: refused, nginx and Caddy left alone" "1 " "$status $(calls)"
[[ "$out" == *"Ports 80 and 443 are used by nginx"* ]] && r=named || r="unclear: $out"
check "streaming on, nginx kept: names nginx" named "$r"
check "streaming on, nginx kept: asked first, Enter = yes" "$pause_question" "$(asked)"
check "streaming on, nginx kept: still off, nothing noted" "off " "$(saved_streaming) $(paused)"

answer=yes port_users="$nginx"
try_switch on
check "streaming on, nginx paused: stopped, also for boot, then Caddy started" \
	"0 is-enabled nginx.service;disable --now nginx.service;enable --now caddy;" "$status $(calls)"
check "streaming on, nginx paused: noted with its start at boot" "nginx.service enabled" "$(paused)"
check "streaming on, nginx paused: saved" on "$(saved_streaming)"
asked >/dev/null

try_switch off
check "streaming off: Caddy stopped first, then nginx started as it was" \
	"0 disable --now caddy;show nginx.service -p LoadState --value;enable --now nginx.service;" "$status $(calls)"
check "streaming off: asked first, Enter = yes" "Start nginx again (paused for streaming)? [y]|" "$(asked)"
check "streaming off: nginx forgotten" "" "$(paused)"

try_switch off
check "streaming off, nothing paused: no question, nothing started" "0 disable --now caddy;|" "$status $(calls)|$(asked)"

nginx_at_boot=disabled port_users="$nginx"
try_switch on
check "nginx not started at boot: paused without disabling" \
	"0 is-enabled nginx.service;stop nginx.service;enable --now caddy;" "$status $(calls)"
check "nginx not started at boot: noted so" "nginx.service disabled" "$(paused)"
asked >/dev/null
answer=no
try_switch off
check "streaming off, answer no: nginx stays off" "0 disable --now caddy;show nginx.service -p LoadState --value;" "$status $(calls)"
[[ "$out" == *"sudo systemctl start nginx.service"* ]] && r=named || r="missing: $out"
check "streaming off, answer no: says how to start it later, as it was" named "$r"
check "streaming off, answer no: nginx forgotten" "" "$(paused)"
asked >/dev/null
answer=yes nginx_at_boot=enabled

port_users="$nginx"$'\n'"python3 session-3.scope 80"
try_switch on
check "nginx paused, python3 keeps port 80: refused, nginx back at once, no Caddy" \
	"1 is-enabled nginx.service;disable --now nginx.service;show nginx.service -p LoadState --value;enable --now nginx.service;" \
	"$status $(calls)"
check "nginx paused, python3 keeps port 80: no offer for python3" "$pause_question" "$(asked)"
check "nginx paused, python3 keeps port 80: still off, nothing noted" "off " "$(saved_streaming) $(paused)"

caddy_fails=yes port_users="$nginx"
try_switch on
check "Caddy does not start: nginx gets the ports back, Caddy stays off at boot" \
	"1 is-enabled nginx.service;disable --now nginx.service;enable --now caddy;disable --now caddy;show nginx.service -p LoadState --value;enable --now nginx.service;" \
	"$status $(calls)"
check "Caddy does not start: still off, nothing noted" "off " "$(saved_streaming) $(paused)"
caddy_fails=no
asked >/dev/null

printf 'nginx.service enabled\n' >"$PAUSED_WEB_SERVERS"
nginx_installed=not-found
try_switch off
check "paused nginx uninstalled meanwhile: not started, not asked" \
	"0 disable --now caddy;show nginx.service -p LoadState --value;|" "$status $(calls)|$(asked)"
[[ "$out" == *"not installed any more"* ]] && r=said || r="not said: $out"
check "paused nginx uninstalled meanwhile: says so" said "$r"
check "paused nginx uninstalled meanwhile: forgotten" "" "$(paused)"
nginx_installed=loaded

printf 'nginx.service enabled\n' >"$PAUSED_WEB_SERVERS"
nginx_at_boot=disabled # as CryptBridge left it when it paused nginx before
(pause_web_server nginx.service) >/dev/null 2>&1
check "paused again: the first note stays, with the state from before" "nginx.service enabled" "$(paused)"
nginx_at_boot=enabled
calls >/dev/null

printf '%s\n' "nginx.service enabled" "--force.service enabled" "broken" >"$PAUSED_WEB_SERVERS"
out="$(resume_web_servers now 2>&1)"
check "unreadable notes: skipped, nothing given to systemctl as an option" \
	"show nginx.service -p LoadState --value;enable --now nginx.service;" "$(calls)"
[[ "$(grep -c "Skipped an unreadable line" <<<"$out")" == 2 ]] && r=warned || r="not warned: $out"
check "unreadable notes: each one named" warned "$r"
check "unreadable notes: forgotten" "" "$(paused)"

port_users=none
switch_streaming on >/dev/null
check "streaming on, ports free: Caddy started and kept on" "enable --now caddy;" "$(calls)"
check "streaming on: saved" on "$(saved_streaming)"
restart_services >/dev/null
check "streaming on: restart includes Caddy" "restart rclone-mount-1.service rclone-webdav.service caddy.service;" "$(calls)"
check "streaming on, ports free: nothing asked" "" "$(asked)"
unset -f require_root systemctl web_port_users confirm asked try_switch paused calls saved_streaming

# --- uninstall: a paused web server is offered back, its ports stay open ----
# A fresh test root each, so that removing CryptBridge spares the others.
for answer in yes no; do
	removed="$(CB_TEST_ROOT="$CB_TEST_ROOT/uninstall-$answer" answer="$answer" bash -s "$here/../setup.sh" 2>&1 <<'EOF'
. "$1"
set +e
trap - ERR
require_root() { :; }
nginx_runs=no
systemctl() {
	printf '%s;' "$*" >>"$CB_TEST_ROOT/calls"
	[[ "$*" == "show nginx.service -p LoadState --value" ]] && echo loaded
	[[ "$*" == "enable --now nginx.service" ]] && nginx_runs=yes
	return 0
}
web_port_users() { [[ "$nginx_runs" == no ]] || echo "nginx nginx.service 80 and 443"; }
ufw() { printf 'ufw %s;' "$*" >>"$CB_TEST_ROOT/calls"; }
confirm() { # removing: yes; starting nginx again: $answer; everything else: Enter
	printf '%s [%s]|' "$1" "$2" >>"$CB_TEST_ROOT/asked"
	case "$1" in
	Remove*) return 0 ;;
	Start*) [[ "$answer" == yes ]] ;;
	*) [[ "$2" == y ]] ;;
	esac
}
mkdir -p "$CONF_DIR" "$(dirname "$CADDYFILE")" "$(dirname "$LOG_FILE")" "$UNIT_DIR"
printf '%s\n' "$MARKER" >"$CADDYFILE"
printf 'nginx.service enabled\n' >"$PAUSED_WEB_SERVERS"
uninstall >/dev/null 2>&1
echo "calls=$(cat "$CB_TEST_ROOT/calls")"
echo "asked=$(cat "$CB_TEST_ROOT/asked")"
[[ -e "$PAUSED_WEB_SERVERS" ]] && echo "note=left" || echo "note=gone"
EOF
	)"
	calls="$(grep '^calls=' <<<"$removed" | cut -d= -f2-)"
	asked="$(grep '^asked=' <<<"$removed" | cut -d= -f2-)"
	name="uninstall, start nginx again: $answer"
	want=not-started
	[[ "$answer" == yes ]] && want=started-after-caddy
	if [[ "$calls" == *"disable --now caddy;"*"enable --now nginx.service;"* ]]; then
		r=started-after-caddy
	elif [[ "$calls" == *"enable --now nginx.service"* ]]; then
		r=started-before-caddy
	else
		r=not-started
	fi
	check "$name: nginx" "$want" "$r"
	[[ "$asked" == *"Start nginx again (paused for streaming)? [y]"* ]] && r=asked || r="not asked: $asked"
	check "$name: asked, Enter = yes" asked "$r"
	want="y closed"
	[[ "$answer" == yes ]] && want="n open"
	r="$(grep -o 'Close ports 80 and 443 in the firewall? \[.\]' <<<"$asked" | grep -o '\[.\]' | tr -d '[]')"
	[[ "$calls" == *"ufw delete allow 80/tcp"*"ufw delete allow 443/tcp"* ]] && r+=" closed" || r+=" open"
	check "$name: firewall question (Enter) keeps the ports open only for the website" "$want" "$r"
	check "$name: nginx forgotten" gone "$(grep '^note=' <<<"$removed" | cut -d= -f2-)"
done
unset removed calls asked

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

# --- choose_user: a number is no user name ----------------------------------
# "id 2" takes the number as a user ID, so it used to pass; the services then
# failed to find a user "2".
r="$( (
	typed=(2 "$(id -un)")
	ask() { # types the next answer; ends the test instead of asking forever
		((${#typed[@]})) || exit 3
		printf -v "$1" '%s' "${typed[0]}"
		typed=("${typed[@]:1}")
	}
	CB_MOUNT_DIR=/vaults
	choose_user >/dev/null 2>"$CB_TEST_ROOT/choose_user.err"
	echo "$CB_USER|$(grep -c "not a number" "$CB_TEST_ROOT/choose_user.err")"
) )"
check "choose_user: a number is refused with a hint, the name typed next is taken" "$(id -un)|1" "$r"

# --- default_command: the installed command opens the menu ------------------
# The texts say "run cryptbridge" for the menu; the downloaded setup.sh (run
# again to update a server) must still set up.
dispatch() { # dispatch NAME — what main runs without a command when started as NAME
	bash -c '. "$1"; menu() { echo menu; }; setup() { echo setup; }; main' "$1" "$here/../setup.sh" 2>&1
}
mkdir -p "$(dirname "$SELF_INSTALLED")"
: >"$SELF_INSTALLED"
check "installed command without a command: menu" menu "$(dispatch "$SELF_INSTALLED")"
check "downloaded setup.sh without a command: setup" setup "$(dispatch "$CB_TEST_ROOT/home/setup.sh")"
check "setup.sh read from a pipe without a command: setup" setup "$(dispatch bash)"
rm -f "$SELF_INSTALLED"
unset -f dispatch

# --- service_title -----------------------------------------------------------
check "title of drive" "Google Drive" "$(CB_SERVICE=drive service_title)"
check "title when no service is known" "the cloud" "$(CB_SERVICE="" service_title)"
check "title of a service the setup does not know" "the cloud" "$(CB_SERVICE=s3 service_title)"

# --- rclone configuration: written, read back, moved from CryptBridge 0.7 ---
as_user() { "$@"; }                       # the tests run as one user
prepare_rclone_dir() { mkdir -p "$RCLONE_DIR"; } # no root for chown
obscure() { printf 'obscured(%s)' "$1"; }
RCLONE_CONF="$RCLONE_DIR/7.conf"
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
# CryptBridge 0.7 to 0.8 (update_single_vault then makes it a vault of its own).
RCLONE_CONF="$SINGLE_RCLONE_CONF"
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
	r="$( (take_transfer "$(printf '{"format":"cryptbridge-transfer-2","name":"Filme & Serien","cloud":{"service":"dropbox","settings":{"token":"T"}},%s}' "$vault_json")" &&
		printf '%s' "$CB_VAULT_NAME") 2>&1)"
	check "format 2 with the vault's name (CryptBridge 0.9): name taken" "Filme & Serien" "$r"
	r="$( (take_transfer "$(block2 '{"service":"dropbox","settings":{"token":"T"}}')" && printf '[%s]' "$CB_VAULT_NAME") 2>&1)"
	check "a block without a name (up to CryptBridge 0.8): no name" "[]" "$r"
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
		r="$( (take_transfer "$(open_transfer_block "$body" "$code")" && printf '%s' "$CB_VAULT_NAME") 2>&1)"
		check "Windows block: the vault's name arrives" 'Filme & Serien "neu"' "$r"
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
