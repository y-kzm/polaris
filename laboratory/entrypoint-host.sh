#!/bin/sh
# Enable RA acceptance and RIO processing.
sysctl -w net.ipv6.conf.eth0.accept_ra=1 2>/dev/null || true
sysctl -w net.ipv6.conf.all.accept_ra=1 2>/dev/null || true
# Accept Route Information Options (RIO) for any prefix length.
# Default is 0 (ignore RIO); 128 allows /0.../128 routes from RA.
sysctl -w net.ipv6.conf.eth0.accept_ra_rt_info_max_plen=128 2>/dev/null || true
sysctl -w net.ipv6.conf.all.accept_ra_rt_info_max_plen=128 2>/dev/null || true
exec sleep infinity
