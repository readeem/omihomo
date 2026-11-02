#!/usr/bin/env python3
import json
import os
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
BINARY = ROOT / 'dist/hosting/tun-check'


def run(*args):
    subprocess.run(args, check=True, timeout=10, stdout=subprocess.DEVNULL)


def private():
    parent = os.environ.get('HOSTING_PARENT_NETNS')
    if not parent or os.readlink('/proc/self/ns/net') == parent:
        raise RuntimeError('refusing host network namespace')
    peer = subprocess.Popen(['unshare', '-n', str(BINARY), 'peer'], stdin=subprocess.PIPE,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        deadline = time.monotonic() + 3
        while os.readlink(f'/proc/{peer.pid}/ns/net') == os.readlink('/proc/self/ns/net'):
            if time.monotonic() > deadline or peer.poll() is not None:
                raise RuntimeError('peer namespace did not start')
            time.sleep(0.01)
        run('ip', 'link', 'set', 'lo', 'up')
        run('ip', 'link', 'add', 'uplink0', 'type', 'veth', 'peer', 'name', 'peer0')
        run('ip', 'addr', 'add', '192.0.2.1/24', 'dev', 'uplink0')
        run('ip', 'link', 'set', 'uplink0', 'up')
        run('ip', 'link', 'set', 'peer0', 'netns', str(peer.pid))
        for command in [('link', 'set', 'lo', 'up'), ('addr', 'add', '192.0.2.2/24', 'dev', 'peer0'), ('link', 'set', 'peer0', 'up'), ('addr', 'add', '203.0.113.2/32', 'dev', 'lo')]:
            run('nsenter', f'--net=/proc/{peer.pid}/ns/net', '--', 'ip', *command)
        run('ip', 'route', 'add', 'default', 'via', '192.0.2.2', 'dev', 'uplink0')
        peer.stdin.write('\n')
        peer.stdin.flush()
        if not select.select([peer.stdout], [], [], 5)[0]:
            raise RuntimeError('peer listener did not become ready')
        line = peer.stdout.readline()
        if not line:
            raise RuntimeError('peer failed: ' + peer.stderr.read(4096))
        port = json.loads(line)['tcp']
        with tempfile.TemporaryDirectory(dir=ROOT / 'dist/hosting', prefix='tun-core-') as work:
            subprocess.run([str(BINARY)], env={**os.environ, 'HOSTING_CORE_DIR': work,
                           'HOSTING_PEER_PORT': str(port)}, check=True, timeout=50)
    finally:
        peer.terminate()
        try:
            peer.communicate(timeout=3)
        except subprocess.TimeoutExpired:
            peer.kill()
            peer.communicate()


def main():
    if '--private' in sys.argv:
        private()
    else:
        subprocess.run(['unshare', '-Urn', sys.executable, __file__, '--private'], cwd=ROOT,
                       env={**os.environ, 'HOSTING_PARENT_NETNS': os.readlink('/proc/self/ns/net')},
                       check=True, timeout=60)


if __name__ == '__main__':
    main()
