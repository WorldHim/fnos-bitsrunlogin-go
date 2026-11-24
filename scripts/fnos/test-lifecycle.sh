#!/bin/bash
#
# Exercises the lifecycle scripts the way fnOS would call them, without a
# fnOS box in the loop.
#
# The tricky part is that the scripts drop privileges and manage a real
# process. To keep this runnable anywhere (the development machine is Windows),
# the harness substitutes a fake binary that sleeps instead of serving HTTP,
# and fakes the user switching. What is actually verified here is the logic
# the scripts own: directory layout, config creation, gateway wiring,
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
#
# It is a script in front of a copy of sleep rather than `exec sleep`: is_running
# identifies the service by /proc/<pid>/comm, which reports the kernel's name for
# the executable, so an exec'd sleep would look like a crashed service. Copying
# sleep to a file called bitsrun keeps the comm right, and `exec -a` cannot help
# because it only rewrites argv[0].
FAKE_STAGE="${WORK}/fake-stage"
mkdir -p "${FAKE_STAGE}"
cp "$(command -v sleep)" "${FAKE_STAGE}/bitsrun"
cat >"${ROOT}/bitsrun" <<'FAKE'
#!/bin/bash
printf '%s\n' "$@" >"$(dirname "$0")/var/argv.txt"
# Act like the real binary: it writes its own pid file. The script passes the
# path after --pid-file, and the value after that is where to write it.
prev=""
for arg in "$@"; do
	if [[ "${prev}" == "--pid-file" ]]; then
		printf '%s\n' "$$" >"${arg}"
	fi
	prev="${arg}"
done
# Re-exec the copied sleep under this process, so the pid stays the same and
# the comm stays bitsrun.
exec "$(dirname "$0")/.fake/bitsrun" 300
FAKE
mkdir -p "${ROOT}/.fake"
cp "${FAKE_STAGE}/bitsrun" "${ROOT}/.fake/bitsrun"
chmod +x "${ROOT}/bitsrun" "${ROOT}/.fake/bitsrun"

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

echo "wizard port is gone"
# The desktop entry points at a gateway path, so a port collected by the
# wizard has nowhere to go. A leftover field would be silently ignored, so
# make its absence explicit.
grep -q 'wizard_port' "${SRC}/wizard/install" 2>/dev/null &&
	bad "install wizard does not ask for a port" ||
	ok "install wizard does not ask for a port"
grep -q 'service_port' "${SRC}/manifest" 2>/dev/null &&
	bad "manifest declares no service port" ||
	ok "manifest declares no service port"

echo "install_callback is idempotent"
before="$(md5sum <"${TRIM_PKGVAR}/Config.yaml")"
bash "${ROOT}/cmd/install_callback" >/dev/null 2>&1
after="$(md5sum <"${TRIM_PKGVAR}/Config.yaml")"
check_eq "${before}" "${after}" "existing config is not overwritten"

echo "status before start"
bash "${MAIN}" status
check_status 3 $? "status reports not running"

echo "start"
bash "${MAIN}" start >/dev/null 2>&1
check_status 0 $? "start succeeds"
bash "${MAIN}" status
check_status 0 $? "status reports running"

# The pid in the file has to be the service itself. Recording the pid of the
# wrapper that was asked to switch users leaves a number that dies at once:
# every later command then believes the service is down, stop kills nothing
# and each start adds another process. A pid that is alive proves the value
# came from the process that is actually serving, not from a wrapper.
echo "pid file names the service process"
recorded_pid="$(cat "${TRIM_PKGVAR}/app.pid" 2>/dev/null)"
[[ -n "${recorded_pid}" ]] && ok "pid file is not empty" || bad "pid file is not empty"
kill -0 "${recorded_pid}" 2>/dev/null && ok "recorded pid is alive" || bad "recorded pid is alive"

echo "arguments passed to the binary"
[[ -f "${TRIM_PKGVAR}/argv.txt" ]] && ok "binary was invoked" || bad "binary was invoked"
grep -qx -- '--server' "${TRIM_PKGVAR}/argv.txt" 2>/dev/null && ok "runs in server mode" || bad "runs in server mode"
grep -qx -- "--server-addr" "${TRIM_PKGVAR}/argv.txt" 2>/dev/null &&
	ok "server address is passed as a flag" ||
	bad "server address is passed as a flag"
grep -qx -- "unix:${ROOT}/app.sock" "${TRIM_PKGVAR}/argv.txt" 2>/dev/null &&
	ok "listens on the gateway socket" ||
	bad "listens on the gateway socket" "$(tr '\n' ' ' <"${TRIM_PKGVAR}/argv.txt" 2>/dev/null)"
grep -qx -- '/app/bitsrunlogin-go' "${TRIM_PKGVAR}/argv.txt" 2>/dev/null &&
	ok "knows the gateway prefix" ||
	bad "knows the gateway prefix" "$(tr '\n' ' ' <"${TRIM_PKGVAR}/argv.txt" 2>/dev/null)"
grep -qx -- "${TRIM_PKGVAR}/Config.yaml" "${TRIM_PKGVAR}/argv.txt" 2>/dev/null &&
	ok "config path points at the persistent file" ||
	bad "config path points at the persistent file"
grep -qx -- '--pid-file' "${TRIM_PKGVAR}/argv.txt" 2>/dev/null &&
	ok "service is told to write its own pid" ||
	bad "service is told to write its own pid"

echo "start is idempotent"
pid_before="$(cat "${TRIM_PKGVAR}/app.pid")"
bash "${MAIN}" start >/dev/null 2>&1
check_status 0 $? "second start succeeds"
check_eq "${pid_before}" "$(cat "${TRIM_PKGVAR}/app.pid")" "second start does not spawn another process"

echo "config_callback restarts the service"
rm -f "${TRIM_PKGVAR}/argv.txt"
bash "${ROOT}/cmd/config_callback" >/dev/null 2>&1
check_status 0 $? "config_callback succeeds"
# The fake binary writes its argv after it is exec'd, so wait for the file to
# reappear rather than assuming the previous run's copy is current.
restarted=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
	if grep -qx -- "--server-addr" "${TRIM_PKGVAR}/argv.txt" 2>/dev/null; then
		restarted=1
		break
	fi
	sleep 1
done
[[ "${restarted}" == "1" ]] &&
	ok "service restarted by the config callback" ||
	bad "service restarted by the config callback" "$(tr '\n' ' ' <"${TRIM_PKGVAR}/argv.txt" 2>/dev/null)"

echo "config_init stops the service"
bash "${ROOT}/cmd/config_init" >/dev/null 2>&1
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

# A service whose pid file went missing must still be stopped. Otherwise the
# process stays on the socket, the next start cannot bind, and the failure
# shows up as a settings change that will not save.
if [[ -r "/proc/$$/comm" ]]; then
	echo "stop recovers a service with no pid file"
	bash "${MAIN}" start >/dev/null 2>&1
	orphan="$(cat "${TRIM_PKGVAR}/app.pid" 2>/dev/null)"
	rm -f "${TRIM_PKGVAR}/app.pid"
	bash "${MAIN}" stop >/dev/null 2>&1
	check_status 0 $? "stop succeeds without a pid file"
	sleep 1
	kill -0 "${orphan}" 2>/dev/null &&
		bad "stray process reclaimed" ||
		ok "stray process reclaimed"
else
	echo "skip stray process test (no /proc)"
fi

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
