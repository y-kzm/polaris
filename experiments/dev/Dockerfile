# A Linux environment for trying single mode on a machine that is not Linux
# (Docker Desktop). See README: the Docker Desktop kernel lacks
# CONFIG_IPV6_ROUTE_INFO, so results obtained here are not kernel RIO behaviour.
FROM ubuntu:24.04
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
        iproute2 iptables tcpdump curl python3 iputils-ping golang-go gcc ca-certificates procps kmod git \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /work/polaris/experiments
CMD ["sleep", "infinity"]
