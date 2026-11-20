"""Shell scripts that build the network.

Single mode: every node is a network namespace on this machine.

    hosts pxh001.. ─┐                       ┌─ px-ia (ISP-A) ─┐
                    ├ br-lan ┬ px-ra (wan) ─┘                 ├ br-core ─ px-srv
                    │        └ px-rb (wan) ─── px-ib (ISP-B) ─┘
    px-ctl ─ br-mgmt ─ px-ra, px-rb          (bridges live in px-sw)

Testbed mode: the routers and the controller are real machines; only the
emulated hosts (namespaces on a bridge) are created here.

The per-node scripts are idempotent, so they are also used to repair a node
after a fault-injection experiment took its interfaces down.
"""

from __future__ import annotations

from .config import Config, RouterCfg, host_mac

SW = "px-sw"
SRV = "px-srv"
CTL = "px-ctl"
LAN_BRIDGE = "br-lan"
SRV_CORE = "fd10:ffff::1"
CTL_MGMT = "fd00:ffff::1"


# A veth pair is created under temporary names and then moved: creating it
# directly under its final name fails if this namespace already has, say, eth0.
#
# Every end gets its address after the move. udev may give an interface an
# address derived from its name (MACAddressPolicy=persistent); since all pairs
# are created as pxtmp0/pxtmp1, several interfaces could otherwise end up with
# the same address on one bridge.
VETH = """pxmac() { printf '06:%s' "$(od -An -N5 -tx1 /dev/urandom | tr -s ' \\n' ':' | sed 's/^://; s/:$//')"; }
veth() {  # namespace1 name1 namespace2-or-empty name2 [mac1]
  ip link add pxtmp0 type veth peer name pxtmp1
  ip link set pxtmp0 netns "$1" name "$2"
  ip -n "$1" link set "$2" address "${5:-$(pxmac)}"
  if [ -n "$3" ]; then
    ip link set pxtmp1 netns "$3" name "$4"
    ip -n "$3" link set "$4" address "$(pxmac)"
  else
    ip link set pxtmp1 name "$4"
    ip link set "$4" address "$(pxmac)"
  fi
}"""


def router_ns(key: str) -> str:
    """Namespace of a router in single mode."""
    return f"px-r{key}"


def isp_ns(key: str) -> str:
    """Namespace of a router's ISP in single mode."""
    return f"px-i{key}"


def host_ns(index: int) -> str:
    """Namespace of emulated host `index` (1-based)."""
    return f"pxh{index:03d}"


def single_links(cfg: Config) -> str:
    """Create the namespaces, bridges and links of single mode (root namespace)."""
    lines = [
        f"ip netns add {SW}",
        f"ip netns exec {SW} sysctl -qw net.ipv6.conf.all.disable_ipv6=1 net.ipv6.conf.default.disable_ipv6=1",
        f"for br in {LAN_BRIDGE} br-mgmt br-core; do",
        f"  ip -n {SW} link add $br type bridge mcast_snooping 0",
        f"  ip -n {SW} link set $br up",
        "done",
        VETH,
        "port() {  # namespace interface port bridge [mac]",
        f'  veth "$1" "$2" {SW} "$3" "${{5:-}}"',
        f'  ip -n {SW} link set "$3" master "$4"',
        f'  ip -n {SW} link set "$3" up',
        '  ip -n "$1" link set lo up',
        '  ip -n "$1" link set "$2" up',
        "}",
        f"ip netns add {SRV}",
        f"ip netns add {CTL}",
        f"port {SRV} eth0 srv-core br-core",
        f"port {CTL} eth0 ctl-mgmt br-mgmt",
    ]
    for r in cfg.routers:
        rns, ins = router_ns(r.key), isp_ns(r.key)
        lines += [
            f"ip netns add {rns}",
            f"ip netns add {ins}",
            f"ip netns exec {rns} sysctl -qw net.ipv6.conf.all.forwarding=1 "
            "net.ipv6.conf.all.accept_ra=0 net.ipv6.conf.default.accept_ra=0",
            f"ip netns exec {ins} sysctl -qw net.ipv6.conf.all.forwarding=1 "
            "net.ipv6.conf.all.accept_ra=0 net.ipv6.conf.default.accept_ra=0",
            f"port {rns} lan r{r.key}-lan {LAN_BRIDGE} 02:00:00:00:{r.key}0:01",
            f"port {rns} mgmt r{r.key}-mgmt br-mgmt",
            f"port {ins} core i{r.key}-core br-core",
            f"veth {rns} wan {ins} cust",
            f"ip -n {rns} link set wan up",
            f"ip -n {ins} link set cust up",
        ]
    return "\n".join(lines)


def router_script(cfg: Config, r: RouterCfg, shared_address: bool = False) -> str:
    """Configure a router (run on the router). Idempotent."""
    lines = ["sysctl -qw net.ipv6.conf.all.forwarding=1"]
    if cfg.mode == "single":
        lines += [
            f"ip link set {r.lan_if} up",
            f"ip link set {r.wan_if} up",
            f"ip -6 addr replace fd10:{r.key}::2/64 dev {r.wan_if} nodad",
            f"ip -6 addr replace {r.mgmt_addr}/64 dev mgmt nodad",
            f"ip -6 route replace {cfg.lan_prefix} dev {r.lan_if}",
            f"ip -6 route replace default via fd10:{r.key}::1 dev {r.wan_if}",
        ]
    if shared_address:
        # Reproduces the submitted version: policy RAs leave from the baseline address.
        lines.append(f"ip -6 addr del {r.policy_ll}/64 dev {r.lan_if} 2>/dev/null || true")
    else:
        lines.append(f"ip -6 addr replace {r.policy_ll}/64 dev {r.lan_if} nodad")
    if cfg.manage_nptv6:
        lines += [
            "for c in PX_POST PX_PRE; do ip6tables -t mangle -N $c 2>/dev/null || ip6tables -t mangle -F $c; done",
            "ip6tables -t mangle -C POSTROUTING -j PX_POST 2>/dev/null || ip6tables -t mangle -A POSTROUTING -j PX_POST",
            "ip6tables -t mangle -C PREROUTING -j PX_PRE 2>/dev/null || ip6tables -t mangle -A PREROUTING -j PX_PRE",
            f"ip6tables -t mangle -A PX_POST -o {r.wan_if} -s {cfg.lan_prefix} -j SNPT "
            f"--src-pfx {cfg.lan_prefix} --dst-pfx {r.isp_prefix}",
            f"ip6tables -t mangle -A PX_PRE -i {r.wan_if} -d {r.isp_prefix} -j DNPT "
            f"--src-pfx {r.isp_prefix} --dst-pfx {cfg.lan_prefix}",
        ]
    return "\n".join(lines)


def router_cleanup(cfg: Config, r: RouterCfg) -> str:
    """Undo router_script on a testbed router."""
    lines = [
        f"ip -6 addr del {r.policy_ll}/64 dev {r.lan_if} 2>/dev/null || true",
        # Left behind if a failover run was killed in the withdraw_lost scenario.
        f"ip6tables -D OUTPUT -o {r.lan_if} -p ipv6-icmp --icmpv6-type 134 -j DROP 2>/dev/null || true",
    ]
    if cfg.manage_nptv6:
        lines += [
            "ip6tables -t mangle -D POSTROUTING -j PX_POST 2>/dev/null || true",
            "ip6tables -t mangle -D PREROUTING -j PX_PRE 2>/dev/null || true",
            "for c in PX_POST PX_PRE; do ip6tables -t mangle -F $c 2>/dev/null || true; "
            "ip6tables -t mangle -X $c 2>/dev/null || true; done",
        ]
    return "\n".join(lines)


def isp_script(r: RouterCfg) -> str:
    """Configure an emulated ISP: routing and ingress filtering (BCP 38). Idempotent."""
    return "\n".join([
        "ip link set cust up",
        "ip link set core up",
        f"ip -6 addr replace fd10:{r.key}::1/64 dev cust nodad",
        f"ip -6 addr replace fd10:ffff::{r.key}/64 dev core nodad",
        f"ip -6 route replace {r.isp_prefix} via fd10:{r.key}::2 dev cust",
        f"ip -6 route replace default via {SRV_CORE} dev core",
        "ip6tables -F FORWARD",
        f"ip6tables -A FORWARD -i cust ! -s {r.isp_prefix} -j DROP",
    ])


def server_script(cfg: Config) -> str:
    """Configure the emulated Internet server: probe addresses and per-ISP return routes."""
    lines = [f"ip -6 addr replace {SRV_CORE}/64 dev eth0 nodad"]
    lines += [f"ip -6 addr replace {dst}/128 dev lo" for dst in sorted({*cfg.d1_dsts, cfg.d1_probe, cfg.default_dst})]
    lines += [f"ip -6 route replace {r.isp_prefix} via fd10:ffff::{r.key} dev eth0" for r in cfg.routers]
    return "\n".join(lines)


def controller_script() -> str:
    """Configure the controller namespace of single mode."""
    return f"ip -6 addr replace {CTL_MGMT}/64 dev eth0 nodad"


def hosts_script(cfg: Config, switch_ns: str, bridge: str) -> str:
    """Create the emulated hosts on a bridge (run in the root namespace)."""
    return f"""
SW="{switch_ns}"
{VETH}
swip() {{ if [ -n "$SW" ]; then ip -n "$SW" "$@"; else ip "$@"; fi; }}
existing=$(ip netns list | awk '{{print $1}}')
i=1
while [ $i -le {cfg.hosts} ]; do
  ns=$(printf 'pxh%03d' $i); port=$(printf 'pxp%03d' $i)
  mac=$(printf '02:00:00:00:%02x:%02x' $((i >> 8)) $((i & 255)))
  if ! echo "$existing" | grep -qx "$ns"; then
    ip netns add $ns
    # Standard host behaviour only. The Route Information Option needs
    # accept_ra_rt_info_max_plen, which Linux leaves at 0 by default.
    ip netns exec $ns sh -c '
      for kv in accept_ra=1 accept_ra_defrtr=1 accept_ra_pinfo=1 accept_ra_rtr_pref=1 \\
                accept_ra_rt_info_max_plen=128 addr_gen_mode=0; do
        sysctl -qw net.ipv6.conf.default.$kv 2>/dev/null || true
      done'
    veth $ns eth0 "$SW" $port $mac
    # A port in the root namespace must not take part in the site's IPv6 (it would accept the RAs).
    [ -n "$SW" ] || sysctl -qw net.ipv6.conf.$port.disable_ipv6=1
    swip link set $port master {bridge}
    swip link set $port up
    ip -n $ns link set lo up
    ip -n $ns link set eth0 up
  fi
  i=$((i + 1))
done
"""


def emulator_bridge_script(cfg: Config) -> str:
    """Create the host bridge on the testbed emulator and attach the uplink NIC."""
    br, uplink = cfg.emulator_bridge, cfg.emulator_uplink
    lines = [
        f"if ! ip link show {br} >/dev/null 2>&1; then",
        f"  ip link add {br} type bridge mcast_snooping 0",
        f"  touch {cfg.workdir}/created-bridge",
        "fi",
        # The emulator itself must not become a host of the site: no addresses or routes from the RAs.
        f"sysctl -qw net.ipv6.conf.{br}.disable_ipv6=1",
        f"ip link set {br} up",
    ]
    if uplink:
        lines += [
            f"sysctl -qw net.ipv6.conf.{uplink}.disable_ipv6=1",
            f"ip link set {uplink} master {br}",
            f"ip link set {uplink} up",
        ]
    return "\n".join(lines)


def kill_namespaces() -> str:
    """Delete every namespace this tool created, with the processes inside."""
    return """
for ns in $(ip netns list | awk '{print $1}' | grep -E '^px(h[0-9]+|-.+)$' || true); do
  ip netns pids $ns 2>/dev/null | xargs -r kill -9 2>/dev/null || true
  ip netns del $ns 2>/dev/null || true
done
"""


__all__ = [
    "CTL", "CTL_MGMT", "LAN_BRIDGE", "SRV", "SRV_CORE", "SW",
    "controller_script", "emulator_bridge_script", "host_mac", "host_ns", "hosts_script",
    "isp_ns", "isp_script", "kill_namespaces", "router_cleanup", "router_ns", "router_script",
    "server_script", "single_links",
]
