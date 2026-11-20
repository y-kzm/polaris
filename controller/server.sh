#!/bin/bash
# Starts the whole RA controller: every module enabled in param.yaml, and the
# backend, which serves the REST API and the web UI on one port.
#
# Usage:
#   ./server.sh                  start everything (builds what is missing)
#   ./server.sh dev              the same, plus the Vite development server
#   ./server.sh build            build the binaries and the UI, then exit
#   ./server.sh backend neighbor start selected components only
#                                (backend, neighbor, endpoint)
#
# Every process gets only its own settings, as environment variables; none of
# them reads param.yaml. The script exits when any process dies, so that
# systemd or Docker restarts the whole set.
#
# Environment variables:
#   PARAM_FILE      configuration file (default: param.yaml next to this script)
#   BIN_DIR         where the binaries are (default: ./bin; built there if missing)
#   STATIC_DIR      built web UI (default: ./frontend/dist; built if missing)
#   DATA_DIR        overrides backend.data_dir
#   BACKEND_PORT    overrides backend.port
#   DB_PATH         overrides the database path (default: <data_dir>/controller.db)
#   FETCH_INTERVAL  overrides backend.fetch_interval
#   LOG_DIR         also write one log file per component there
#   NO_COLOR        set to 1 to disable ANSI colour

set -e
cd "$(dirname "$0")"
ROOT=$(pwd)

PARAM_FILE=${PARAM_FILE:-$ROOT/param.yaml}
BIN_DIR=${BIN_DIR:-$ROOT/bin}
STATIC_DIR=${STATIC_DIR:-$ROOT/frontend/dist}
START_TIMEOUT=60   # seconds a process may take to start listening

# ── Colour / logging ──────────────────────────────────────────────────────────

if [[ -t 1 && "${NO_COLOR:-0}" != "1" ]]; then _USE_COLOR=1; else _USE_COLOR=0; fi
_C_RESET=0; _C_DIM=2; _C_RED=31; _C_GREEN=32; _C_YELLOW=33; _C_BLUE=34; _C_MAGENTA=35; _C_CYAN=36
_esc() { [[ $_USE_COLOR -eq 1 ]] && printf '\033[%sm' "$1" || true; }

_log() {  # colour label message
  local ts; ts="$(date '+%Y-%m-%d %H:%M:%S')"
  printf '%s%s%s %s[%s]%s %s\n' "$(_esc $_C_DIM)" "$ts" "$(_esc $_C_RESET)" "$(_esc "$1")" "$2" "$(_esc $_C_RESET)" "$3"
  if [[ -n "${LOG_DIR:-}" ]]; then printf '%s [%s] %s\n' "$ts" "$2" "$3" >> "$LOG_DIR/$2.log"; fi
}
log_info()  { _log $_C_GREEN server "$*"; }
log_error() { _log $_C_RED server "ERROR: $*" >&2; }
die()       { log_error "$*"; exit 1; }

# log_pipe LABEL COLOUR: prefixes every line of stdin with a timestamp and the label.
log_pipe() {
  local label=$1 color=$2 line
  while IFS= read -r line; do _log "$color" "$label" "$line"; done
}

# ── Configuration (param.yaml) ────────────────────────────────────────────────

[[ -f "$PARAM_FILE" ]] || die "configuration file not found: $PARAM_FILE"
python3 -c 'import yaml' 2>/dev/null \
  || die "Python 3 with PyYAML is needed to read param.yaml (apt install python3-yaml, or pip install pyyaml)"

# yaml_get KEY [DEFAULT]: scalar at a dotted key.
yaml_get() {
  python3 - "$PARAM_FILE" "$1" "${2:-}" <<'EOF'
import sys, yaml
value = yaml.safe_load(open(sys.argv[1])) or {}
for key in sys.argv[2].split("."):
    value = value.get(key) if isinstance(value, dict) else None
if isinstance(value, bool):
    print("1" if value else "")
elif isinstance(value, list):
    print(",".join(str(v) for v in value))
else:
    print(sys.argv[3] if value is None else value)
EOF
}

# yaml_json KEY: list or mapping at a dotted key, as JSON ("[]" if absent).
yaml_json() {
  python3 - "$PARAM_FILE" "$1" <<'EOF'
import json, sys, yaml
value = yaml.safe_load(open(sys.argv[1])) or {}
for key in sys.argv[2].split("."):
    value = value.get(key) if isinstance(value, dict) else None
print(json.dumps([] if value is None else value))
EOF
}

# enabled_modules PREFIX: "name port" of every enabled module whose name starts with PREFIX.
enabled_modules() {
  python3 - "$PARAM_FILE" "$1" <<'EOF'
import sys, yaml
config = yaml.safe_load(open(sys.argv[1])) or {}
for name, cfg in (config.get("modules") or {}).items():
    if name.startswith(sys.argv[2]) and (cfg or {}).get("enabled", False):
        if not cfg.get("port"):
            sys.exit(f"module {name} is enabled but has no port")
        print(name, cfg["port"])
EOF
}

BACKEND_PORT=${BACKEND_PORT:-$(yaml_get backend.port 8080)}
BACKEND_HOST=$(yaml_get backend.listen_host "")
DATA_DIR=${DATA_DIR:-$(yaml_get backend.data_dir "$ROOT/data")}
FETCH_INTERVAL=${FETCH_INTERVAL:-$(yaml_get backend.fetch_interval 10)}
RECONCILE_INTERVAL=${RECONCILE_INTERVAL:-$(yaml_get backend.reconcile_interval 60)}
CORS_ORIGINS=${CORS_ORIGINS:-$(yaml_get backend.cors_origins "")}
mkdir -p "$DATA_DIR"
DATA_DIR=$(cd "$DATA_DIR" && pwd)
DB_PATH=${DB_PATH:-$DATA_DIR/controller.db}

if [[ -n "${LOG_DIR:-}" ]]; then
  mkdir -p "$LOG_DIR"; LOG_DIR="$(cd "$LOG_DIR" && pwd)"
  log_info "file logging → $LOG_DIR/"
fi

# ── What to run ───────────────────────────────────────────────────────────────

RUN_NEIGHBOR=1; RUN_ENDPOINT=1; RUN_BACKEND=1; RUN_DEV=0; BUILD_ONLY=0
if [[ $# -gt 0 && "$1" != "dev" && "$1" != "build" ]]; then
  RUN_NEIGHBOR=0; RUN_ENDPOINT=0; RUN_BACKEND=0
fi
for arg in "$@"; do
  case "$arg" in
    dev)      RUN_DEV=1 ;;
    build)    BUILD_ONLY=1 ;;
    neighbor) RUN_NEIGHBOR=1 ;;
    endpoint) RUN_ENDPOINT=1 ;;
    backend)  RUN_BACKEND=1 ;;
    *) die "unknown argument: $arg (use: dev, build, backend, neighbor, endpoint)" ;;
  esac
done

MODULES=()   # "name port" of every module to start
for kind in neighbor endpoint; do
  if [[ $kind == neighbor && $RUN_NEIGHBOR -eq 0 ]] || [[ $kind == endpoint && $RUN_ENDPOINT -eq 0 ]]; then continue; fi
  listed=$(enabled_modules "${kind}_collector") || die "param.yaml: check the modules section"
  while read -r line; do [[ -n "$line" ]] && MODULES+=("$line"); done <<< "$listed"
done

# hostport HOST PORT: "host:port", with an IPv6 host in brackets.
hostport() { if [[ "$1" == *:* ]]; then echo "[$1]:$2"; else echo "$1:$2"; fi; }
# local_host HOST: an address this machine can use to reach a listener on HOST.
local_host() { case "$1" in ""|0.0.0.0) echo 127.0.0.1 ;; ::) echo ::1 ;; *) echo "$1" ;; esac; }

# ── Build ─────────────────────────────────────────────────────────────────────

# build_go NAME DIR PACKAGE: (re)build bin/NAME if it is missing or older than its sources.
# A prebuilt binary without sources (the container image) is used as it is.
build_go() {
  local name=$1 dir=$2 pkg=$3 bin="$BIN_DIR/$1"
  if [[ ! -d "$dir" ]]; then
    [[ -x "$bin" ]] || die "$name: neither the binary $bin nor the sources $dir exist"
    return
  fi
  if [[ -x "$bin" && -z "$(find "$dir" \( -name '*.go' -o -name go.mod -o -name go.sum \) -newer "$bin" -print -quit)" ]]; then
    return
  fi
  command -v go >/dev/null || die "$name has to be built, but Go is not installed (or use: docker compose up)"
  log_info "building $name"
  mkdir -p "$BIN_DIR"
  (cd "$dir" && go build -o "$bin" "$pkg") || die "building $name failed"
}

build_ui() {
  if [[ "$STATIC_DIR" != "$ROOT/frontend/dist" ]]; then   # prebuilt elsewhere
    [[ -f "$STATIC_DIR/index.html" ]] || die "STATIC_DIR $STATIC_DIR has no index.html"
    return
  fi
  local src="$ROOT/frontend"
  if [[ -f "$STATIC_DIR/index.html" && -z "$(find "$src/src" "$src/index.html" "$src/package.json" -newer "$STATIC_DIR/index.html" -print -quit 2>/dev/null)" ]]; then
    return
  fi
  command -v npm >/dev/null || die "the web UI has to be built, but Node.js is not installed (or use: docker compose up)"
  log_info "building the web UI"
  (cd "$src" && { [[ -d node_modules ]] || npm ci --no-audit --no-fund; } && npm run build) >/dev/null \
    || die "building the web UI failed (run 'npm run build' in frontend/ to see why)"
}

for entry in "${MODULES[@]}"; do
  name=${entry%% *}
  build_go "${name//_/-}" "$ROOT/modules/${name//_/-}" .
done
if [[ $RUN_BACKEND -eq 1 ]]; then
  build_go ra-controller "$ROOT/backend" ./cmd/server
  build_ui
fi
if [[ $BUILD_ONLY -eq 1 ]]; then log_info "build finished"; exit 0; fi

# ── Process management ────────────────────────────────────────────────────────

pids=(); labels=()

kill_tree() {
  local pid=$1 child
  for child in $(pgrep -P "$pid" 2>/dev/null || true); do kill_tree "$child"; done
  kill "$pid" 2>/dev/null || true
}

cleanup() {
  trap '' EXIT INT TERM
  log_info "stopping all services..."
  for pid in "${pids[@]}"; do kill_tree "$pid"; done
  wait 2>/dev/null || true
  log_info "stopped"
}
trap cleanup EXIT
# Being asked to stop is not a failure.
trap 'cleanup; exit 0' INT TERM

# run LABEL COLOUR COMMAND...: start a process in the background with labelled output.
run() {
  local label=$1 color=$2; shift 2
  ( "$@" 2>&1 | log_pipe "$label" "$color" ) &
  pids+=($!); labels+=("$label")
}

# wait_for_port HOST PORT LABEL: wait until the process started last listens, or fail after START_TIMEOUT.
wait_for_port() {
  local host; host=$(local_host "$1")
  local port=$2 label=$3 pid=${pids[${#pids[@]}-1]} waited=0
  until bash -c "(: < /dev/tcp/$host/$port) 2>/dev/null"; do
    kill -0 "$pid" 2>/dev/null || die "$label exited while starting (see its messages above)"
    [[ $waited -lt $((START_TIMEOUT * 5)) ]] || die "$label did not listen on port $port within ${START_TIMEOUT}s"
    sleep 0.2; waited=$((waited + 1))
  done
  log_info "$label is ready (port $port)"
}

# ── Modules ───────────────────────────────────────────────────────────────────

NEIGHBOR_API_URLS=""; NEIGHBOR_SOURCE_NAMES=""; FQDN_API_BASES=""

start_module() {
  local name=$1 port=$2
  local bin="$BIN_DIR/${name//_/-}" key="modules.$name"
  # Modules are reachable from the backend only, unless listen_host says otherwise.
  local host; host=$(yaml_get "$key.listen_host" 127.0.0.1)
  local color=$_C_CYAN; [[ $name == endpoint_* ]] && color=$_C_YELLOW
  local addr; addr=$(hostport "$host" "$port")
  log_info "starting $name on $addr"
  case "$name" in
    neighbor_collector_snmp)
      run "$name" $color env ADDR="$addr" \
        SNMP_TARGETS="$(yaml_json "$key.snmp_targets")" \
        POLL_INTERVAL="$(yaml_get "$key.poll_interval" 30)" \
        STALE_POLLS="$(yaml_get "$key.stale_polls" 3)" \
        TARGET_TIMEOUT="$(yaml_get "$key.target_timeout" 20)" \
        ALLOW_TARGET_REGISTRATION="$(yaml_get "$key.allow_target_registration" "")" \
        "$bin" ;;
    neighbor_collector_static)
      run "$name" $color env ADDR="$addr" STATIC_DATA="$(yaml_json "$key.neighbors")" "$bin" ;;
    endpoint_collector_static)
      run "$name" $color env ADDR="$addr" STATIC_DATA="$(yaml_json "$key.services")" "$bin" ;;
    endpoint_collector_ms365)
      run "$name" $color env ADDR="$addr" \
        DB_PATH="$DATA_DIR/$(basename "$(yaml_get "$key.db_path" m365_endpoints.db)")" \
        M365_INSTANCE="$(yaml_get "$key.instance" Worldwide)" \
        REFRESH_INTERVAL="$(yaml_get "$key.refresh_interval" 3600)" \
        "$bin" ;;
    *)
      run "$name" $color env ADDR="$addr" "$bin" ;;
  esac
  wait_for_port "$host" "$port" "$name"
  # The backend reaches a module that listens on every interface through the loopback address.
  local via; via=$(hostport "$(local_host "$host")" "$port")
  if [[ $name == neighbor_* ]]; then
    NEIGHBOR_API_URLS="${NEIGHBOR_API_URLS:+$NEIGHBOR_API_URLS,}http://$via/api/neighbors"
    NEIGHBOR_SOURCE_NAMES="${NEIGHBOR_SOURCE_NAMES:+$NEIGHBOR_SOURCE_NAMES,}${name//_/-}"
  else
    FQDN_API_BASES="${FQDN_API_BASES:+$FQDN_API_BASES,}http://$via/api/fqdn/services"
  fi
}

for entry in "${MODULES[@]}"; do
  start_module ${entry}
done

# ── Backend (REST API and web UI) ─────────────────────────────────────────────

if [[ $RUN_BACKEND -eq 1 ]]; then
  # The Vite development server is another origin for the API.
  if [[ $RUN_DEV -eq 1 ]]; then CORS_ORIGINS="${CORS_ORIGINS:+$CORS_ORIGINS,}http://localhost:5173"; fi
  log_info "starting backend on ${BACKEND_HOST:-*}:$BACKEND_PORT"
  run backend $_C_BLUE env \
    LISTEN_ADDR="$(hostport "$BACKEND_HOST" "$BACKEND_PORT")" \
    DB_PATH="$DB_PATH" \
    STATIC_DIR="$STATIC_DIR" \
    CORS_ORIGINS="$CORS_ORIGINS" \
    FETCH_INTERVAL="$FETCH_INTERVAL" \
    RECONCILE_INTERVAL="$RECONCILE_INTERVAL" \
    NEIGHBOR_API_URLS="$NEIGHBOR_API_URLS" \
    NEIGHBOR_SOURCE_NAMES="$NEIGHBOR_SOURCE_NAMES" \
    FQDN_API_BASES="$FQDN_API_BASES" \
    "$BIN_DIR/ra-controller"
  wait_for_port "$BACKEND_HOST" "$BACKEND_PORT" backend
  log_info "web UI and API: http://localhost:$BACKEND_PORT"
fi

if [[ $RUN_DEV -eq 1 ]]; then
  log_info "starting the Vite development server (frontend with hot reload)"
  run frontend $_C_MAGENTA env BACKEND_PORT="$BACKEND_PORT" npm --prefix "$ROOT/frontend" run dev -- --host
fi

[[ ${#pids[@]} -gt 0 ]] || die "nothing to start: no module is enabled and the backend was not selected"
log_info "all services started — press Ctrl+C to stop"

# Stop everything as soon as one process is gone: a half-running controller is
# worse than a restart by systemd or Docker.
while true; do
  for i in "${!pids[@]}"; do
    if ! kill -0 "${pids[$i]}" 2>/dev/null; then
      log_error "${labels[$i]} exited; stopping the others"
      exit 1
    fi
  done
  sleep 1
done
