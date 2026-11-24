#!/bin/bash
#
# Shared helpers for the lifecycle scripts.
#
# fnOS sources its own environment into these scripts, but it does not
# guarantee that every variable exists (a hand-run `cmd/main start` during
# debugging, for instance, has TRIM_* unset). Everything here falls back to a
# sane value so the scripts stay usable outside the appcenter too.
#
# Sourced, never executed: `source "$(dirname "$0")/common.sh"`.

set -u

APP_NAME="bitsrunlogin-go"

# --- directories ------------------------------------------------------------

# The installation root. $0 lives in {root}/cmd, so the parent of the script
# directory is the root when TRIM_APPDEST is unavailable.
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
: "${TRIM_APPDEST:=$(cd -- "${SCRIPT_DIR}/.." && pwd)}"

# The app's own directory is target/, which is what TRIM_APPDEST points at.
# The payload is not nested under an app/ directory: fnpack stores the payload
# as app.tgz and the installer extracts its contents straight into this root,
# so the binary that was staged at app/bitsrun lands at bitsrun.
APP_DEST="${TRIM_APPDEST}"
APP_BIN="${APP_DEST}/bitsrun"

# Writable directories. Configuration, logs and the pid file must survive an
# upgrade, and target/ is replaced wholesale during one, so they never live
# there. TRIM_PKGVAR is the documented place for data that must persist.
: "${TRIM_PKGVAR:=${APP_DEST}/var}"
: "${TRIM_PKGETC:=${APP_DEST}/etc}"

CONFIG_FILE="${TRIM_PKGVAR}/Config.yaml"
PID_FILE="${TRIM_PKGVAR}/app.pid"
LOG_FILE="${TRIM_PKGVAR}/info.log"
BITS_LOG_DIR="${TRIM_PKGVAR}/logs"

# The fnOS gateway forwards to a Unix socket inside the installed app directory.
# A loopback TCP port would not work: the desktop opens the entry in an iframe
# rendered by the user's browser, where 127.0.0.1 is the user's own machine and
# not the NAS.
GATEWAY_SOCKET="${APP_DEST}/app.sock"
GATEWAY_PREFIX="/app/${APP_NAME}"
SERVER_ADDR="unix:${GATEWAY_SOCKET}"

# Runtime user. The package declares run-as=package, so the scripts drop to
# the package user explicitly rather than relying on how fnOS invokes them.
: "${TRIM_USERNAME:=${APP_NAME}}"
: "${TRIM_RUN_USERNAME:=${TRIM_USERNAME}}"

# --- logging ----------------------------------------------------------------

# echo_to_logs appends a timestamped line to the installer log. Failures are
# swallowed: a logging problem must never abort an install.
echo_to_logs() {
	mkdir -p "$(dirname -- "${LOG_FILE}")" 2>/dev/null &&
		printf '%s - [%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "${APP_NAME}" "$*" >>"${LOG_FILE}" 2>/dev/null
	return 0
}

# fail logs and exits non-zero, which aborts the install step in the appcenter.
fail() {
	echo_to_logs "ERROR: $*"
	echo "ERROR: $*" >&2
	exit 1
}

# --- user helpers -----------------------------------------------------------

# as_app_user runs a command as the package user when possible.
#
# `runuser` and `su` both need root to switch users. When the script is
# already running as the package user (which is the normal case: run-as is
# package) neither is needed and the command runs directly. Falling back
# rather than failing means the same script works under both privilege models.
as_app_user() {
	local current
	current="$(id -un 2>/dev/null || echo unknown)"
	if [[ "${current}" == "${TRIM_RUN_USERNAME}" || "${EUID:-0}" -ne 0 ]]; then
		"$@"
		return $?
	fi

	if command -v runuser >/dev/null 2>&1; then
		runuser -u "${TRIM_RUN_USERNAME}" -- "$@"
	elif command -v su >/dev/null 2>&1; then
		su -s /bin/sh "${TRIM_RUN_USERNAME}" -c "$(printf '%q ' "$@")"
	else
		"$@"
	fi
}

# ensure_dirs creates the writable directories and hands them to the package
# user, because these scripts may have created them as root.
ensure_dirs() {
	mkdir -p "${TRIM_PKGVAR}" "${BITS_LOG_DIR}" "${TRIM_PKGETC}" || return 1

	if [[ "${EUID:-0}" -eq 0 ]] && id -u "${TRIM_USERNAME}" >/dev/null 2>&1; then
		local group
		group="$(id -gn "${TRIM_USERNAME}" 2>/dev/null || echo "${TRIM_USERNAME}")"
		chown -R "${TRIM_USERNAME}:${group}" "${TRIM_PKGVAR}" "${TRIM_PKGETC}" 2>/dev/null || true
	fi

	# The config file holds the campus network password and, if configured,
	# DDNS API tokens. Nobody but the package user should be able to read it.
	if [[ -f "${CONFIG_FILE}" ]]; then
		chmod 600 "${CONFIG_FILE}" 2>/dev/null || true
	fi
	return 0
}

# --- configuration ----------------------------------------------------------

# config_uses_file_logging reports whether the config asks for a log file.
config_uses_file_logging() {
	[[ -f "${CONFIG_FILE}" ]] || return 1
	grep -Eq '^[[:space:]]*write_file:[[:space:]]*true' "${CONFIG_FILE}" 2>/dev/null
}

# write_default_config writes the configuration the binary ships with, but
# pointed at the persistent directory instead of the working directory.
#
# This is hand written rather than produced by the binary because the binary
# writes its default to the current working directory and exits, which the
# lifecycle script cannot use.
#
# form.domain is left empty on purpose. The upstream default points at a
# Microsoft connectivity-check page, which is almost never the campus portal;
# shipping it would look like a configured install and send the first login to
# the wrong host. An empty value makes the service stay idle and the UI ask for
# the real address instead.
write_default_config() {
	[[ -f "${CONFIG_FILE}" ]] && return 0

	cat >"${CONFIG_FILE}" <<-'EOF'
		form:
		  domain: ""
		  username: ""
		  usertype: "cmcc"
		  password: ""
		meta:
		  n: "200"
		  type: "1"
		  acid: "5"
		  enc: "srun_bx1"
		  os: "Windows 10"
		  name: "windows"
		  info_prefix: "SRBX1"
		  double_stack: false
		settings:
		  basic:
		    https: false
		    skip_cert_verify: false
		    timeout: 5
		    interfaces: ""
		    interfaces_interval: 0
		    dns_server: ""
		  guardian:
		    enable: true
		    duration: 300
		  backoff:
		    enable: false
		    max_retry: 0
		    initial_duration: 2
		    max_duration: 300
		    exponent_factor: 1
		    inter_const_factor: 0
		    outer_const_factor: 0
		  log:
		    debug_level: false
		    write_file: true
		    log_path: ""
		    log_name: ""
		  ddns:
		    enable: false
		    ttl: 600
		    domain: "www.example.com"
		    provider: "cloudflare"
		  reality:
		    enable: false
		    addr: "http://www.baidu.com"
		  custom_header:
		    User-Agent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36"
	EOF

	chmod 600 "${CONFIG_FILE}" 2>/dev/null || true
	echo_to_logs "wrote default configuration to ${CONFIG_FILE}"
	return 0
}

# set_log_path points the config's log directory at the persistent one.
#
# Writing logs next to the binary would put them in target/, which an upgrade
# replaces. The server also keeps an in-memory ring buffer for the log page,
# so the file is only needed when the user asks for it.
set_log_path() {
	[[ -f "${CONFIG_FILE}" ]] || return 0
	local current
	current="$(sed -nE 's/^[[:space:]]*log_path:[[:space:]]*"(.*)"[[:space:]]*$/\1/p' "${CONFIG_FILE}" | head -n1)"
	# Only rewrite the value the package itself writes; anything else means
	# the user chose it deliberately.
	if [[ -z "${current}" || "${current}" == "./" || "${current}" == "${BITS_LOG_DIR}" ]]; then
		sed -i -E "s#^([[:space:]]*log_path:).*#\\1 \"${BITS_LOG_DIR}\"#" "${CONFIG_FILE}" 2>/dev/null || true
	fi
	return 0
}

# --- gateway ----------------------------------------------------------------

# The service listens on a Unix socket rather than a port. fnOS publishes it at
# GATEWAY_PREFIX and forwards there, which is what lets the desktop entry work
# from a browser on another machine.

# clear_gateway_socket removes a socket left behind by a previous run, so a
# restart does not trip over it. A socket that still answers is left alone.
clear_gateway_socket() {
	[[ -S "${GATEWAY_SOCKET}" ]] || return 0
	if command -v socat >/dev/null 2>&1; then
		socat -u "OPEN:${GATEWAY_SOCKET}" /dev/null 2>/dev/null && return 0
	fi
	rm -f "${GATEWAY_SOCKET}" 2>/dev/null || true
	return 0
}

# --- process state ----------------------------------------------------------

# read_pid prints the pid recorded in the pid file, if the process is alive.
read_pid() {
	[[ -r "${PID_FILE}" ]] || return 1
	local pid
	pid="$(head -n1 "${PID_FILE}" 2>/dev/null | tr -d '[:space:]')"
	[[ -n "${pid}" ]] || return 1
	if kill -0 "${pid}" 2>/dev/null; then
		printf '%s' "${pid}"
		return 0
	fi
	rm -f "${PID_FILE}" 2>/dev/null
	return 1
}

# is_running reports whether the service process is alive.
is_running() {
	local pid
	if pid="$(read_pid)"; then
		# Guard against a recycled pid: the process name must still match.
		# /proc/<pid>/comm is used rather than cmdline because cmdline has no
		# trailing newline, and feeding it to a text tool blocks until the
		# read reaches the end of the buffer. The file may be unreadable
		# (older kernels, restricted /proc), in which case the pid file is
		# the only evidence available and is trusted.
		if [[ -r "/proc/${pid}/comm" ]]; then
			local comm
			comm="$(<"/proc/${pid}/comm")" 2>/dev/null || return 0
			[[ "${comm}" == *bitsrun* ]] && return 0
			return 1
		fi
		return 0
	fi
	return 1
}

# stop_process terminates the service and waits for it to exit.
#
# SIGTERM is the same signal a `kill` sends by hand: the server treats it as a
# request to shut the login loop down cleanly, so an in-flight login is not
# cut off mid request. Only a process that ignores it is killed outright.
stop_process() {
	local stopped=1
	local pid
	if pid="$(read_pid)"; then
		echo_to_logs "stopping pid ${pid}"
		kill -TERM "${pid}" 2>/dev/null || true

		local waited=0
		while kill -0 "${pid}" 2>/dev/null && ((waited < 15)); do
			sleep 1
			waited=$((waited + 1))
		done

		if kill -0 "${pid}" 2>/dev/null; then
			echo_to_logs "pid ${pid} did not exit after ${waited}s, sending SIGKILL"
			kill -KILL "${pid}" 2>/dev/null || true
			sleep 1
		fi
		rm -f "${PID_FILE}" 2>/dev/null
	else
		echo_to_logs "no usable pid file"
		stopped=0
	fi

	# The pid file can be missing or stale while the service is still up: an
	# interrupted install, a removed var directory, or a pid that was reused.
	# Leaving those processes behind is what made every later start fail with
	# "address already in use", so sweep by name as a last resort.
	kill_leftovers

	echo_to_logs "stopped"
	[[ "${stopped}" == "1" ]] || echo_to_logs "cleaned up stray processes"
	return 0
}

# kill_leftovers stops any remaining process of this app, identified by its
# executable name. It is deliberately narrow: the pattern has to match the
# app's own binary so unrelated processes are never touched.
kill_leftovers() {
	local pids
	pids="$(pids_of_app 2>/dev/null || true)"
	[[ -n "${pids}" ]] || return 0

	echo_to_logs "leftover process(es): ${pids}"
	# shellcheck disable=SC2086
	kill -TERM ${pids} 2>/dev/null || true

	local waited=0
	local remaining
	while ((waited < 10)); do
		remaining="$(pids_of_app 2>/dev/null || true)"
		[[ -n "${remaining}" ]] || return 0
		sleep 1
		waited=$((waited + 1))
	done

	# shellcheck disable=SC2086
	kill -KILL ${remaining} 2>/dev/null || true
	sleep 1
	return 0
}

# pids_of_app prints the pids whose command line runs this app's binary. The
# pid in the pid file is included even when its process name no longer matches,
# because a genuinely running service must never be left unpoliced.
pids_of_app() {
	local found=""
	local dir
	for dir in /proc/[0-9]*; do
		[[ -r "${dir}/comm" ]] || continue
		local comm
		comm="$(<"${dir}/comm")" 2>/dev/null || continue
		[[ "${comm}" == *bitsrun* ]] || continue
		found="${found} ${dir#/proc/}"
	done
	printf '%s' "${found# }"
}

# start_process launches the service in the background.
start_process() {
	if is_running; then
		echo_to_logs "already running (pid $(read_pid))"
		return 0
	fi

	[[ -x "${APP_BIN}" ]] || fail "binary missing or not executable: ${APP_BIN}"

	# A socket left by a previous run makes the bind fail, so clear it first.
	clear_gateway_socket

	echo_to_logs "starting on ${SERVER_ADDR}, published at ${GATEWAY_PREFIX}"

	# The server appends its own output here; the web UI reads the same
	# information from the in-memory log buffer, so this file is for
	# troubleshooting when the service will not start at all.
	#
	# The process must not inherit this script's stdout: the appcenter reads
	# it, and a background child holding the pipe open makes the caller wait
	# for the daemon to exit. Redirecting to the log file and closing the
	# extra descriptors before the fork keeps `start` returning immediately.
	#
	# `cd` runs in this shell rather than a subshell so the background job is
	# a child of the script and $! names it.
	local dir
	dir="$(pwd)"
	cd "${TRIM_PKGVAR}" || {
		echo_to_logs "cannot enter ${TRIM_PKGVAR}"
		return 1
	}

	# $! is the pid of as_app_user, not of the service: when it has to switch
	# users, runuser or su forks the real process and exits, so $! names a
	# process that is already gone. Recording that pid made every later check
	# believe the service was down: stop left the old process running, start
	# spawned another one, and each settings change added a process until the
	# socket was held by a stale instance.
	#
	# The service is asked to record its own pid instead. dash and bash both
	# keep $$ stale in a `sh -c` subshell on some paths, so a wrapper shell
	# cannot be trusted to report the right number; the binary writes the pid
	# file itself on startup (see internal/server), and that number is
	# unambiguous because it comes from the very process that serves requests.
	rm -f "${PID_FILE}" 2>/dev/null
	as_app_user "${APP_BIN}" \
		--server \
		--server-addr "${SERVER_ADDR}" \
		--gateway-prefix "${GATEWAY_PREFIX}" \
		--config "${CONFIG_FILE}" \
		--pid-file "${PID_FILE}" \
		>>"${LOG_FILE}" 2>&1 </dev/null &

	cd "${dir}" || true

	# Give the process a moment to create the pid file so a failure is
	# reported here instead of surfacing later as a blank iframe. The pid
	# file is written by the service itself, so give it a moment to appear
	# before the first check, and drop the mode to owner-only once it exists.
	local waited=0
	while ((waited < 10)); do
		if [[ -s "${PID_FILE}" ]]; then
			chmod 600 "${PID_FILE}" 2>/dev/null || true
		fi
		if is_running; then
			echo_to_logs "started (pid $(read_pid))"
			return 0
		fi
		sleep 1
		waited=$((waited + 1))
	done

	echo_to_logs "process did not stay up, see ${LOG_FILE}"
	return 1
}
