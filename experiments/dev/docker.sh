#!/bin/sh
# Start (or enter) a privileged Linux container with this repository mounted.
#   dev/docker.sh                 open a shell
#   dev/docker.sh ./exp setup     run one command
# GO_RA_DIR selects the go-ra checkout (default: ../agent/go-ra).
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
repo=$(dirname "$here")
gora=${GO_RA_DIR:-$repo/agent/go-ra}
name=polaris-exp-dev
if ! docker ps --format '{{.Names}}' | grep -qx "$name"; then
    docker rm -f "$name" >/dev/null 2>&1 || true
    docker build -q -t "$name" "$here/dev" >/dev/null
    docker run -d --name "$name" --privileged \
        -v "$repo":/work/polaris -v "$(cd "$gora" && pwd)":/work/polaris/agent/go-ra \
        -v polaris-exp-work:/var/tmp/polaris-exp "$name" >/dev/null
fi
# The neighbour table limit is a setting of the Docker VM's kernel, not of a
# container. 200 hosts need more than the default; raise it until Docker restarts.
docker run --rm --privileged --pid=host "$name" nsenter -t 1 -n sh -c '
    p=/proc/sys/net/ipv6/neigh/default
    [ "$(cat $p/gc_thresh3)" -ge 16384 ] || { echo 4096 > $p/gc_thresh1; echo 8192 > $p/gc_thresh2; echo 16384 > $p/gc_thresh3; }
' >/dev/null 2>&1 || echo "warning: could not raise the neighbour table limit of the Docker VM" >&2
if [ $# -eq 0 ]; then
    exec docker exec -it "$name" bash
fi
exec docker exec "$name" "$@"
