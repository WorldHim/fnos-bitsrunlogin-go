#!/bin/bash
#
# Exercises the lifecycle scripts the way fnOS would call them, without a
# fnOS box in the loop.
#
# The tricky part is that the scripts drop privileges and manage a real
# process. To keep this runnable anywhere (the development machine is Windows),
# the harness substitutes a fake binary that sleeps instead of serving HTTP,
# and fakes the user switching. What is actually verified here is the logic
# the scripts own: directory layout, config creation, port resolution,
# start/stop/status exit codes, and idempotency under repeated calls.
#
# Usage: scripts/fnos/test-lifecycle.sh [--keep]
#
# Exit code 0 when every check passes.

set -uo pipefail

KEEP=0
[[ "${1:-}" == "--keep" ]] && KEEP=1

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC="${REPO_ROOT}/scripts/fnos/project"
WORK="$(mktemp -d 2>/dev/null || mktemp -d -t fnostest)"

pass=0
fail=0

cleanup() {
	if [[ "${KEEP}" == "1" ]]; then
		echo "kept work dir: ${WORK}"
		return
	fi
	# Never leave a stray process behind even when a check failed early.
	[[ -r "${WORK}/app/var/app.pid" ]] && kill -KILL "$(cat "${WORK}/app/var/app.pid")" 2>/dev/null
	rm -rf "${WORK}"
}
trap cleanup EXIT

ok() {
	pass=$((pass + 1))
	printf '  ok   %s\n' "$1"
}

bad() {
	fail=$((fail + 1))
	printf '  FAIL %s\n' "$1"
	if [[ -n "${2:-}" ]]; then printf '       %s\n' "$2"; fi
}

check_eq() {
	local want="$1" got="$2" what="$3"
	if [[ "${want}" == "${got}" ]]; then ok "${what}"; else bad "${what}" "want '${want}', got '${got}'"; fi
}

check_status() {
	local want="$1" got="$2" what="$3"
	if [[ "${want}" == "${got}" ]]; then ok "${what}"; else bad "${what}" "want exit ${want}, got ${got}"; fi
}

# --- build a fake install tree ---------------------------------------------

ROOT="${WORK}/app"
mkdir -p "${ROOT}/cmd" "${ROOT}/var"
cp "${SRC}/cmd/"* "${ROOT}/cmd/"
chmod +x "${ROOT}/cmd/"*

# A stand-in for the Go binary: it records the arguments it was given and then
# waits, so the process state machine can be observed. The binary sits directly
# in the install root, next to var/, so the path is one level down.
cat >"${ROOT}/bitsrun" <<'FAKE'
#!/bin/bash
printf '%s\n' "$@" >"$(dirname "$0")/var/argv.txt"
exec sleep 300
FAKE
chmod +x "${ROOT}/bitsrun"

export TRIM_APPDEST="${ROOT}"
export TRIM_PKGVAR="${ROOT}/var"
export TRIM_PKGETC="${ROOT}/etc"
export TRIM_USERNAME="$(id -un)"
export TRIM_RUN_USERNAME="$(id -un)"
export TRIM_APPVER="1.6.8"

MAIN="${ROOT}/cmd/main"

echo "install_callback"
bash "${ROOT}/cmd/install_callback" >/dev/null 2>&1
check_status 0 $? "install_callback succeeds"
[[ -f "${TRIM_PKGVAR}/Config.yaml" ]] && ok "config created in the persistent dir" || bad "config created in the persistent dir"
grep -q 'log_path: "'"${TRIM_PKGVAR}"'/logs"' "${TRIM_PKGVAR}/Config.yaml" &&
	ok "log path points at the persistent dir" ||
	bad "log path points at the persistent dir" "$(grep log_path "${TRIM_PKGVAR}/Config.yaml")"
[[ -d "${TRIM_PKGVAR}/logs" ]] && ok "log dir created" || bad "log dir created"
check_eq "9877" "$(cat "${TRIM_PKGVAR}/port")" "default port recorded"

echo "install_callback is idempotent"
before="$(md5sum <"${TRIM_PKGVAR}/Config.yaml")"
bash "${ROOT}/cmd/install_callback" >/dev/null 2>&1
after="$(md5sum <"${TRIM_PKGVAR}/Config.yaml")"
check_eq "${before}" "${after}" "existing config is not overwritten"

echo "wizard port is honoured"
rm -f "${TRIM_PKGVAR}/port"
wizard_port="18080" bash "${ROOT}/cmd/install_callback" >/dev/null 2>&1
check_eq "18080" "$(cat "${TRIM_PKGVAR}/port")" "port from the wizard"

echo "invalid wizard port falls back"
rm -f "${TRIM_PKGVAR}/port"
wizard_port="not-a-port" bash "${ROOT}/cmd/install_callback" >/dev/null 2>&1
check_eq "9877" "$(cat "${TRIM_PKGVAR}/port")" "invalid port rejected"
rm -f "${TRIM_PKGVAR}/port"
wizard_port="70000" bash "${ROOT}/cmd/install_callback" >/dev/null 2>&1
check_eq "9877" "$(cat "${TRIM_PKGVAR}/port")" "out of range port rejected"

echo "status before start"
bash "${MAIN}" status
check_status 3 $? "status reports not running"

echo "start"
bash "${MAIN}" start >/dev/null 2>&1
check_status 0 $? "start succeeds"
bash "${MAIN}" status
check_status 0 $? "status reports running"

echo "arguments passed to the binary"
[[ -f "${TRIM_PKGVAR}/argv.txt" ]] && ok "binary was invoked" || bad "binary was invoked"
grep -qx -- '--server' "${TRIM_PKGVAR}/argv.txt" 2>/dev/null && ok "runs in server mode" || bad "runs in server mode"
grep -qx -- '127.0.0.1:9877' "${TRIM_PKGVAR}/argv.txt" 2>/dev/null &&
	ok "listens on the loopback default port" ||
	bad "listens on the loopback default port" "$(tr '\n' ' ' <"${TRIM_PKGVAR}/argv.txt" 2>/dev/null)"
grep -qx -- "${TRIM_PKGVAR}/Config.yaml" "${TRIM_PKGVAR}/argv.txt" 2>/dev/null &&
	ok "config path points at the persistent file" ||
	bad "config path points at the persistent file"

echo "start is idempotent"
pid_before="$(cat "${TRIM_PKGVAR}/app.pid")"
bash "${MAIN}" start >/dev/null 2>&1
check_status 0 $? "second start succeeds"
check_eq "${pid_before}" "$(cat "${TRIM_PKGVAR}/app.pid")" "second start does not spawn another process"

echo "config_callback restarts on a new port"
rm -f "${TRIM_PKGVAR}/argv.txt"
wizard_port="18081" bash "${ROOT}/cmd/config_callback" >/dev/null 2>&1
check_status 0 $? "config_callback succeeds"
check_eq "18081" "$(cat "${TRIM_PKGVAR}/port")" "new port recorded"
# The fake binary writes its argv after it is exec'd, so wait for the file to
# contain the new port rather than merely to exist: a stale copy from the
# previous start satisfies an existence check but proves nothing.
restarted=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
	if grep -qx -- '127.0.0.1:18081' "${TRIM_PKGVAR}/argv.txt" 2>/dev/null; then
		restarted=1
		break
	fi
	sleep 1
done
[[ "${restarted}" == "1" ]] &&
	ok "service restarted on the new port" ||
	bad "service restarted on the new port" "$(tr '\n' ' ' <"${TRIM_PKGVAR}/argv.txt" 2>/dev/null)"

echo "config_init stops the service"
wizard_port="18082" bash "${ROOT}/cmd/config_init" >/dev/null 2>&1
check_status 0 $? "config_init succeeds"
bash "${MAIN}" status
check_status 3 $? "service stopped for the change"

echo "stop"
bash "${MAIN}" start >/dev/null 2>&1
bash "${MAIN}" stop >/dev/null 2>&1
check_status 0 $? "stop succeeds"
bash "${MAIN}" status
check_status 3 $? "status reports not running"
[[ -f "${TRIM_PKGVAR}/app.pid" ]] && bad "pid file removed on stop" || ok "pid file removed on stop"

echo "stop when already stopped"
bash "${MAIN}" stop >/dev/null 2>&1
check_status 0 $? "stop is idempotent"

echo "stale pid file"
mkdir -p "${TRIM_PKGVAR}"
printf '999999\n' >"${TRIM_PKGVAR}/app.pid"
bash "${MAIN}" status
check_status 3 $? "stale pid is not treated as running"
[[ -f "${TRIM_PKGVAR}/app.pid" ]] && bad "stale pid file cleaned up" || ok "stale pid file cleaned up"

echo "recycled pid is not our process"
sleep 300 &
decoy=$!
printf '%s\n' "${decoy}" >"${TRIM_PKGVAR}/app.pid"
bash "${MAIN}" status
status=$?
kill -KILL "${decoy}" 2>/dev/null
wait "${decoy}" 2>/dev/null
# The name check needs /proc, which this harness may not have (MSYS on
# Windows does not). Without it the pid file is the only evidence available
# and a live pid counts as running, which is the safe direction: the service
# must never be started twice.
if [[ -r "/proc/$$/comm" ]]; then
	check_status 3 "${status}" "a live pid with a foreign name is not confused for the service"
else
	check_status 0 "${status}" "without /proc a live pid is trusted (no double start)"
fi
rm -f "${TRIM_PKGVAR}/app.pid"

echo "unknown argument"
bash "${MAIN}" bogus >/dev/null 2>&1
check_status 1 $? "unknown action fails"

echo "missing binary"
mv "${ROOT}/bitsrun" "${ROOT}/bitsrun.off"
bash "${MAIN}" start >/dev/null 2>&1
check_status 1 $? "start fails when the binary is missing"
mv "${ROOT}/bitsrun.off" "${ROOT}/bitsrun"

echo
printf '%d passed, %d failed\n' "${pass}" "${fail}"
[[ "${fail}" -eq 0 ]]
