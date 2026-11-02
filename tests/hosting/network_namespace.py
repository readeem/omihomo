#!/usr/bin/env python3
import os
from pathlib import Path
import subprocess


def main():
    root = Path(__file__).resolve().parents[2]
    script = '''set -eu
ip link set lo up
ip addr add 192.0.2.1/24 dev lo
ip addr add 192.0.2.2/32 dev lo
ip tuntap add dev capture mode tun
ip link set capture up
ip route add default dev capture table 100
ip rule add priority 100 lookup local
ip rule del priority 0
ip rule add priority 1 oif lo lookup local
ip rule add priority 2 to 192.0.2.2/32 lookup 100
HOSTING_PRIVATE_POLICY=1 go test -count=1 -timeout=15s -run '^TestPrivatePolicyCapture$' -v ./tests/hosting/publisher
'''
    subprocess.run(['unshare', '-Urn', 'sh', '-c', script], cwd=root,
                   env={**os.environ, 'HOSTING_PARENT_NETNS': os.readlink('/proc/self/ns/net')}, check=True, timeout=60)


if __name__ == '__main__':
    main()
