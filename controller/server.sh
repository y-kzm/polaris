#!/bin/bash
# Orchestrates all RA Controller services.
#
# Usage:
#   ./server.sh                      # start all enabled modules + backend + frontend
#   ./server.sh backend neighbor     # start selected components only
#
# Components:
#   neighbor   all enabled neighbor_collector_* modules  (see param.yaml)
#   endpoint   all enabled endpoint_collector_* modules  (see param.yaml)
#   backend    RA Controller backend
#   frontend   Vite dev server
#
# Environment variables:
#   LOG_DIR          Write per-component log files here (optional; journald is used under systemd)
#   NO_COLOR         Set to 1 to disable ANSI colour (auto-disabled when stdout is not a TTY)
#   BACKEND_PORT     Override backend.port from param.yaml
#   DB_PATH          Override backend.db_path from param.yaml
#   FETCH_INTERVAL   Override backend.fetch_interval from param.yaml
#   NEIGHBOR_IFNAME  Override backend.neighbor_ifname from param.yaml

set -e
cd "$(dirname "$0")"

# ── Colour / logging ──────────────────────────────────────────────────────────

# Disable colour automatically when stdout is not a terminal.
if [[ -t 1 && "${NO_COLOR:-0}" != "1" ]]; then
  _USE_COLOR=1
else
  _USE_COLOR=0
fi

# Colour codes (used via _esc)
_C_RESET=0; _C_DIM=2
_C_RED=31; _C_GREEN=32; _C_YELLOW=33; _C_BLUE=34; _C_MAGENTA=35; _C_CYAN=36

_esc() { [[ $_USE_COLOR -eq 1 ]] && printf '\033[%sm' "$1" || true; }

# log_info / log_error: server-level messages with green/red [server] tag.
log_info() {
  local ts; ts="$(date '+%Y-%m-%d %H:%M:%S')"
  printf '%s%s%s %s[server]%s %s\n' \
    "$(_esc $_C_DIM)" "$ts" "$(_esc $_C_RESET)" \
    "$(_esc $_C_GREEN)" "$(_esc $_C_RESET)" \
    "$*"
  if [[ -n "${LOG_DIR:-}" ]]; then
    printf '%s [server] %s\n' "$ts" "$*" >> "$LOG_DIR/server.log"
  fi
}

log_error() {
  local ts; ts="$(date '+%Y-%m-%d %H:%M:%S')"
  printf '%s%s%s %s[server]%s ERROR: %s\n' \
    "$(_esc $_C_DIM)" "$ts" "$(_esc $_C_RESET)" \
    "$(_esc $_C_RED)" "$(_esc $_C_RESET)" \
    "$*" >&2
  if [[ -n "${LOG_DIR:-}" ]]; then
    printf '%s [server] ERROR: %s\n' "$ts" "$*" >> "$LOG_DIR/server.log"
  fi
}

# log_pipe LABEL COLOR
# Reads stdin line by line, prefixes each with a timestamp and colour-coded label.
# If LOG_DIR is set, also appends to $LOG_DIR/<label>.log.
log_pipe() {
  local label=$1 color=$2
  while IFS= read -r line; do
    local ts; ts="$(date '+%Y-%m-%d %H:%M:%S')"
    printf '%s%s%s %s[%s]%s %s\n' \
      "$(_esc $_C_DIM)" "$ts" "$(_esc $_C_RESET)" \
      "$(_esc $color)" "$label" "$(_esc $_C_RESET)" \
      "$line"
    if [[ -n "${LOG_DIR:-}" ]]; then
      printf '%s [%s] %s\n' "$ts" "$label" "$line" >> "$LOG_DIR/${label}.log"
    fi
  done
}

# ── YAML helpers ──────────────────────────────────────────────────────────────

yaml_get() {
  python3 - "$1" <<'EOF'
import sys, yaml
with open("param.yaml") as f:
    d = yaml.safe_load(f)
v = d
for k in sys.argv[1].split("."):
    v = v.get(k) if isinstance(v, dict) else None
print("" if v is None else v)
EOF
}

enabled_modules() {
  python3 - "$1" <<'EOF'
import sys, yaml
with open("param.yaml") as f:
    d = yaml.safe_load(f)
prefix = sys.argv[1]
for name, cfg in (d.get("modules") or {}).items():
    if name.startswith(prefix) and (cfg or {}).get("enabled", False):
        print(name, cfg.get("port", ""))
EOF
}

module_config_json() {
  python3 - "$1" "$2" <<'EOF'
import sys, yaml, json
with open("param.yaml") as f:
    d = yaml.safe_load(f)
cfg = (d.get("modules") or {}).get(sys.argv[1]) or {}
print(json.dumps(cfg.get(sys.argv[2], [])))
EOF
}

# ── Config ────────────────────────────────────────────────────────────────────

BACKEND_PORT=${BACKEND_PORT:-$(yaml_get backend.port)}
DB_PATH=${DB_PATH:-$(yaml_get backend.db_path)}
FETCH_INTERVAL=${FETCH_INTERVAL:-$(yaml_get backend.fetch_interval)}
NEIGHBOR_IFNAME=${NEIGHBOR_IFNAME:-$(yaml_get backend.neighbor_ifname)}

# Resolve LOG_DIR to an absolute path so it survives cd's inside subshells.
if [[ -n "${LOG_DIR:-}" ]]; then
  mkdir -p "$LOG_DIR"
  LOG_DIR="$(cd "$LOG_DIR" && pwd)"
  log_info "file logging → $LOG_DIR/"
fi

# ── Component selection ───────────────────────────────────────────────────────

if [[ $# -eq 0 ]]; then
  RUN_NEIGHBOR=1; RUN_ENDPOINT=1; RUN_BACKEND=1; RUN_FRONTEND=1
else
  RUN_NEIGHBOR=0; RUN_ENDPOINT=0; RUN_BACKEND=0; RUN_FRONTEND=0
  for arg in "$@"; do
    case "$arg" in
      neighbor) RUN_NEIGHBOR=1 ;;
      endpoint) RUN_ENDPOINT=1 ;;
      backend)  RUN_BACKEND=1 ;;
      frontend) RUN_FRONTEND=1 ;;
      *)
        log_error "unknown component: $arg"
        echo "Available: neighbor  endpoint  backend  frontend" >&2
        exit 1
        ;;
    esac
  done
fi

pids=()
NEIGHBOR_PORTS=()
NEIGHBOR_NAMES=()
FQDN_PORTS=()

# ── Process management ────────────────────────────────────────────────────────

kill_tree() {
  local pid=$1
  local children; children=$(pgrep -P "$pid" 2>/dev/null || true)
  for child in $children; do kill_tree "$child"; done
  kill "$pid" 2>/dev/null || true
}

cleanup() {
  trap '' EXIT INT TERM
  echo ""
  log_info "stopping all services..."
  for pid in "${pids[@]}"; do kill_tree "$pid"; done
  wait 2>/dev/null || true
  log_info "stopped"
}
trap cleanup EXIT INT TERM

wait_for_port() {
  local port=$1 label=$2
  local ts; ts="$(date '+%Y-%m-%d %H:%M:%S')"
  printf '%s%s%s %s[server]%s waiting for %s...' \
    "$(_esc $_C_DIM)" "$ts" "$(_esc $_C_RESET)" \
    "$(_esc $_C_GREEN)" "$(_esc $_C_RESET)" "$label"
  until bash -c "(: < /dev/tcp/localhost/$port) 2>/dev/null"; do sleep 0.2; done
  echo " ready"
}

# Returns the ANSI colour code to use for a given module name.
_module_color() {
  case "$1" in
    neighbor_*) echo $_C_CYAN    ;;  # cyan   — neighbor collectors
    endpoint_*) echo $_C_YELLOW  ;;  # yellow — endpoint collectors
    *)          echo 37          ;;  # white
  esac
}

# Start a module and pipe its output through log_pipe.
start_module() {
  local name=$1 port=$2
  local dir="modules/${name//_/-}"
  local color; color=$(_module_color "$name")
  log_info "starting $name on :$port"
  case "$name" in
    endpoint_collector_ms365)
      local db; db=$(yaml_get "modules.${name}.db_path")
      local inst; inst=$(yaml_get "modules.${name}.instance")
      (ADDR=":$port" DB_PATH="$db" M365_INSTANCE="$inst" bash "$dir/start.sh" 2>&1 | log_pipe "$name" "$color") &
      ;;
    endpoint_collector_static)
      local data; data=$(module_config_json "$name" "services")
      (ADDR=":$port" STATIC_DATA="$data" bash "$dir/start.sh" 2>&1 | log_pipe "$name" "$color") &
      ;;
    neighbor_collector_static)
      local data; data=$(module_config_json "$name" "neighbors")
      (ADDR=":$port" STATIC_DATA="$data" bash "$dir/start.sh" 2>&1 | log_pipe "$name" "$color") &
      ;;
    neighbor_collector_snmp)
      local targets; targets=$(module_config_json "$name" "snmp_targets")
      local poll; poll=$(yaml_get "modules.${name}.poll_interval")
      (ADDR=":$port" SNMP_TARGETS="$targets" POLL_INTERVAL="$poll" bash "$dir/start.sh" 2>&1 | log_pipe "$name" "$color") &
      ;;
    *)
      (ADDR=":$port" bash "$dir/start.sh" 2>&1 | log_pipe "$name" "$color") &
      ;;
  esac
  pids+=($!)
}

# ── Start collectors ──────────────────────────────────────────────────────────

if [[ $RUN_NEIGHBOR -eq 1 ]]; then
  while read -r name port; do
    start_module "$name" "$port"
    NEIGHBOR_PORTS+=("$port")
    NEIGHBOR_NAMES+=("${name//_/-}")
  done < <(enabled_modules "neighbor_collector")
fi

if [[ $RUN_ENDPOINT -eq 1 ]]; then
  while read -r name port; do
    start_module "$name" "$port"
    FQDN_PORTS+=("$port")
  done < <(enabled_modules "endpoint_collector")
fi

for port in "${NEIGHBOR_PORTS[@]}"; do
  wait_for_port "$port" "neighbor (port $port)"
done
for port in "${FQDN_PORTS[@]}"; do
  wait_for_port "$port" "endpoint (port $port)"
done

# ── Backend ───────────────────────────────────────────────────────────────────

if [[ $RUN_BACKEND -eq 1 ]]; then
  NEIGHBOR_API_URLS=""
  for port in "${NEIGHBOR_PORTS[@]}"; do
    NEIGHBOR_API_URLS="${NEIGHBOR_API_URLS:+$NEIGHBOR_API_URLS,}http://localhost:$port/api/neighbors"
  done

  NEIGHBOR_SOURCE_NAMES=""
  for name in "${NEIGHBOR_NAMES[@]}"; do
    NEIGHBOR_SOURCE_NAMES="${NEIGHBOR_SOURCE_NAMES:+$NEIGHBOR_SOURCE_NAMES,}$name"
  done

  FQDN_API_BASES=""
  for port in "${FQDN_PORTS[@]}"; do
    FQDN_API_BASES="${FQDN_API_BASES:+$FQDN_API_BASES,}http://localhost:$port/api/fqdn/services"
  done

  log_info "starting backend on :$BACKEND_PORT"
  (
    cd backend
    PORT="$BACKEND_PORT" \
    DB_PATH="$DB_PATH" \
    FETCH_INTERVAL="$FETCH_INTERVAL" \
    NEIGHBOR_IFNAME="$NEIGHBOR_IFNAME" \
    NEIGHBOR_API_URLS="$NEIGHBOR_API_URLS" \
    NEIGHBOR_SOURCE_NAMES="$NEIGHBOR_SOURCE_NAMES" \
    FQDN_API_BASES="$FQDN_API_BASES" \
    bash start.sh 2>&1 | log_pipe "backend" "$_C_BLUE"
  ) &
  pids+=($!)
  wait_for_port "$BACKEND_PORT" "backend"
fi

# ── Frontend ──────────────────────────────────────────────────────────────────

if [[ $RUN_FRONTEND -eq 1 ]]; then
  log_info "starting frontend (Vite dev server)"
  (cd frontend && BACKEND_PORT="$BACKEND_PORT" npm run dev -- --host 2>&1 | log_pipe "frontend" "$_C_MAGENTA") &
  pids+=($!)
fi

log_info "all services started — press Ctrl+C to stop"
wait "${pids[@]}"
