#!/bin/sh
# End host of the lab. Nothing Polaris-specific is configured: the host only
# runs standard Router Advertisement processing.

# Docker installs a static default route via the bridge gateway. Remove it so
# that the host's default routers come only from Router Advertisements.
ip -6 route show default | while read -r route; do
    case "$route" in
        *"proto ra"*) ;;
        *) ip -6 route del $route 2>/dev/null || true ;;
    esac
done

# Route Information Options (RFC 4191) need kernel support.
RIO_SYSCTL=/proc/sys/net/ipv6/conf/eth0/accept_ra_rt_info_max_plen
if [ ! -e "$RIO_SYSCTL" ]; then
    # Kernel built without CONFIG_IPV6_ROUTE_INFO (e.g. Docker Desktop).
    rio-shim eth0 &
elif [ "$(cat "$RIO_SYSCTL")" = "0" ]; then
    # Linux ignores the option by default. ./lab up sets the sysctl through
    # docker-compose.kernel-rio.yaml; this happens when compose is run by hand.
    echo "WARNING: accept_ra_rt_info_max_plen is 0, RIO routes will be ignored. Start the lab with ./lab up." >&2
fi

exec sleep infinity
