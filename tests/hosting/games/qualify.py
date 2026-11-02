#!/usr/bin/env python3
import asyncio
import contextlib
import ctypes
import hashlib
import json
import os
from pathlib import Path
import secrets
import ssl
import sys
import tempfile
import time
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from experiment import Engine, command, unused_ports, VIRTUAL, BINARY_SHA256

ROOT = Path(__file__).resolve().parents[3]
ASSETS = ROOT / 'dist/hosting/games'


def keys(text):
    x = ctypes.CDLL('libX11.so.6')
    t = ctypes.CDLL('libXtst.so.6')
    x.XOpenDisplay.restype = ctypes.c_void_p
    x.XOpenDisplay.argtypes = [ctypes.c_char_p]
    x.XStringToKeysym.argtypes = [ctypes.c_char_p]
    x.XStringToKeysym.restype = ctypes.c_ulong
    x.XKeysymToKeycode.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
    t.XTestFakeKeyEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
    x.XFlush.argtypes = [ctypes.c_void_p]
    x.XCloseDisplay.argtypes = [ctypes.c_void_p]
    display = x.XOpenDisplay(os.environ['DISPLAY'].encode())
    assert display
    x.XDefaultRootWindow.argtypes = [ctypes.c_void_p]
    x.XDefaultRootWindow.restype = ctypes.c_ulong
    x.XQueryTree.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.POINTER(ctypes.c_ulong), ctypes.POINTER(ctypes.c_ulong), ctypes.POINTER(ctypes.POINTER(ctypes.c_ulong)), ctypes.POINTER(ctypes.c_uint)]
    x.XSetInputFocus.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.c_int, ctypes.c_ulong]
    root, parent, children, count = ctypes.c_ulong(), ctypes.c_ulong(), ctypes.POINTER(ctypes.c_ulong)(), ctypes.c_uint()
    x.XQueryTree(display, x.XDefaultRootWindow(display), ctypes.byref(root), ctypes.byref(parent), ctypes.byref(children), ctypes.byref(count))
    assert count.value
    x.XSetInputFocus(display, children[count.value - 1], 1, 0)
    x.XFlush(display)
    try:
        for c in text:
            symbol = {'\n': 'Return', '`': 'grave', ' ': 'space'}.get(c, c)
            code = x.XKeysymToKeycode(display, x.XStringToKeysym(symbol.encode()))
            assert code
            t.XTestFakeKeyEvent(display, code, 1, 0)
            t.XTestFakeKeyEvent(display, code, 0, 0)
            x.XFlush(display)
            time.sleep(.08)
        x.XFlush(display)
    finally:
        x.XCloseDisplay(display)


async def run(directory, game):
    engines, processes, logs = [], [], []
    xray = ROOT / 'dist/hosting/xray'
    assert hashlib.sha256(xray.read_bytes()).hexdigest() == BINARY_SHA256
    env = dict(os.environ, HOME=str(directory), XDG_CONFIG_HOME=str(directory),
               LD_LIBRARY_PATH=str(ASSETS / 'tools/usr/lib'), LIBGL_ALWAYS_SOFTWARE='1',
               SDL_VIDEODRIVER='x11', XDG_RUNTIME_DIR=str(directory))
    env.pop('WAYLAND_DISPLAY', None)

    async def spawn(name, *args):
        log = open(directory / (name + '.log'), 'wb')
        logs.append(log)
        p = await asyncio.create_subprocess_exec(*map(str, args), env=env,
            stdin=asyncio.subprocess.PIPE, stdout=log, stderr=log)
        processes.append(p)
        return p

    async def seen(name, needle, timeout=40):
        async with asyncio.timeout(timeout):
            while True:
                data = (directory / (name + '.log')).read_text(errors='replace')
                if needle in data:
                    return
                if any(p.returncode not in (None, 0) for p in processes):
                    raise RuntimeError('game process failed: inspect private logs')
                await asyncio.sleep(.1)

    cover = None
    try:
        cert, key = directory / 'cert.pem', directory / 'key.pem'
        await command('openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt',
            'ec_paramgen_curve:P-256', '-nodes', '-keyout', key, '-out', cert,
            '-days', '1', '-subj', '/CN=relay.example', '-addext', 'subjectAltName=DNS:relay.example')
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(cert, key)
        tls.set_alpn_protocols(['h2', 'http/1.1'])
        async def camouflage(r, w):
            w.close()
            await w.wait_closed()
        cover = await asyncio.start_server(camouflage, '127.0.0.1', 0, ssl=tls)
        reality, consumer = unused_ports()
        local, front = unused_ports()
        publisher, friend = str(uuid.uuid4()), str(uuid.uuid4())
        pair = dict(line.split(': ', 1) for line in (await command(xray, 'x25519')).splitlines())
        short = secrets.token_hex(8)
        relay_config = {'log': {'loglevel': 'info'}, 'inbounds': [
            {'tag': 'publisher', 'listen': '127.0.0.1', 'port': reality, 'protocol': 'vless',
             'settings': {'decryption': 'none', 'clients': [{'id': publisher, 'reverse': {'tag': 'published'}}]},
             'streamSettings': {'network': 'raw', 'security': 'reality', 'realitySettings': {
                 'target': '127.0.0.1:' + str(cover.sockets[0].getsockname()[1]),
                 'serverNames': ['relay.example'], 'privateKey': pair['PrivateKey'], 'shortIds': [short]}}},
            {'tag': 'consumer', 'listen': '127.0.0.1', 'port': consumer, 'protocol': 'vless',
             'settings': {'decryption': 'none', 'clients': [{'id': friend}]},
             'streamSettings': {'network': 'raw', 'security': 'tls', 'tlsSettings': {
                 'certificates': [{'certificateFile': str(cert), 'keyFile': str(key)}]}}}],
            'outbounds': [{'tag': 'reject', 'protocol': 'blackhole'}],
            'routing': {'rules': [{'type': 'field', 'inboundTag': ['consumer'], 'outboundTag': 'published'}]}}
        protocol = 'tcp' if game == 'openttd' else 'udp'
        exposed = 3979 if game == 'openttd' else 30000
        host_config = {'credential': publisher, 'config': {'relay': {'version': 1, 'label': 'Game fixture',
            'host': '127.0.0.1', 'port': reality, 'address': VIRTUAL, 'reality': {
                'server_name': 'relay.example', 'public_key': pair['Password (PublicKey)'], 'short_id': short}},
            'shares': [{'id': 'game', 'label': game, 'local_address': '127.0.0.1', 'local_port': local,
                'exposed_port': exposed, 'protocol': protocol, 'enabled': True}]}}
        client_config = {'log': {'loglevel': 'info'}, 'inbounds': [{'listen': '127.0.0.1',
            'port': front, 'protocol': 'dokodemo-door', 'settings': {
                'address': VIRTUAL, 'port': exposed, 'network': protocol}}],
            'outbounds': [{'protocol': 'vless', 'settings': {'vnext': [{'address': '127.0.0.1',
                'port': consumer, 'users': [{'id': friend, 'encryption': 'none'}]}]},
                'streamSettings': {'network': 'raw', 'security': 'tls', 'tlsSettings': {
                    'serverName': 'relay.example', 'certificates': [{'certificateFile': str(cert), 'usage': 'verify'}]}}}]}
        for name, executable, config, adapter in [
            ('relay', xray, relay_config, False),
            ('host', ROOT / 'dist/hosting/publisher', host_config, True),
            ('consumer', xray, client_config, False)]:
            e = Engine(executable, directory, name, config, adapter)
            engines.append(e)
            await e.start()
        await engines[0].event('v1.rvs.cool', 15)
        display_read, display_write = os.pipe()
        log = open(directory / 'xvfb.log', 'wb'); logs.append(log)
        xvfb = await asyncio.create_subprocess_exec(str(ASSETS/'tools/usr/bin/Xvfb'),
            '-displayfd', str(display_write), '-screen', '0', '1024x768x24', '-nolisten', 'tcp',
            '-ac', env=env, pass_fds=(display_write,), stdout=log, stderr=log)
        processes.append(xvfb); os.close(display_write)
        number = await asyncio.wait_for(asyncio.to_thread(os.read, display_read, 32), 10)
        os.close(display_read)
        assert number.strip().isdigit(), 'Xvfb did not become ready'
        env['DISPLAY'] = ':' + number.decode().strip()
        os.environ['DISPLAY'] = env['DISPLAY']
        if game == 'openttd':
            binary = ASSETS / 'openttd-build/openttd'
            config = directory / 'server.cfg'
            config.write_text(f'[network]\nserver_port = {local}\nserver_game_type = local\nserver_name = Fixture\n[server_bind_addresses]\n127.0.0.1 =\n[game_creation]\nmap_x = 6\nmap_y = 6\n')
            server = await spawn('server', binary, '-D', '-c', config, '-g', '-d', 'net=3')
            await seen('server', 'Map generated')
            client_config_path = directory / 'client.cfg'
            client_config_path.write_text('[network]\nclient_name = FixturePlayer\n')
            await spawn('client', binary, '-c', client_config_path, '-n', f'127.0.0.1:{front}', '-s', 'null', '-m', 'null', '-d', 'net=3')
            await seen('server', 'joined as FixturePlayer')
            keys('`say fixtureplayed\n')
            await seen('server', 'fixtureplayed')
        else:
            binary = ASSETS / 'luanti-5.18.0/bin/luanti'
            world = directory / 'world'; world.mkdir()
            (world/'world.mt').write_text('gameid = devtest\nbackend = sqlite3\nplayer_backend = sqlite3\nauth_backend = sqlite3\n')
            config = directory / 'luanti.conf'
            config.write_text(f'bind_address = 127.0.0.1\nport = {local}\nserver_announce = false\nenable_sound = false\nvideo_driver = opengl3\n')
            await spawn('server', binary, '--server', '--config', config, '--world', world)
            await seen('server', 'Server for gameid="devtest"')
            await spawn('client', binary, '--config', config, '--go', '--address', '127.0.0.1', '--port', str(front), '--name', 'FixturePlayer')
            await seen('server', 'joins game')
            await seen('client', 'meant only for testing')
            keys('tfixtureplayed\n')
            await seen('server', 'fixtureplayed')
        print('PASS', game, 'official client joined and sent in-game chat through official Xray + embedded adapter', flush=True)
    finally:
        for p in reversed(processes):
            if p.returncode is None:
                p.terminate()
                try: await asyncio.wait_for(p.wait(), 5)
                except TimeoutError:
                    p.kill(); await p.wait()
        for e in reversed(engines): await e.stop()
        if cover:
            cover.close(); await cover.wait_closed()
        for log in logs: log.close()


def main():
    os.umask(0o077)
    interfaces = [line.split(':')[0].strip() for line in Path('/proc/net/dev').read_text().splitlines()[2:]]
    assert interfaces == ['lo'], 'run in private netns with only loopback'
    assert any(line.split()[1:3] == ['/tmp', 'tmpfs'] for line in Path('/proc/mounts').read_text().splitlines()), 'mount private tmpfs on /tmp'
    game = sys.argv[1]
    assert game in ('openttd', 'luanti')
    directory = Path(tempfile.mkdtemp(prefix=game+'-', dir=ASSETS))
    print('Private evidence directory:', directory.relative_to(ROOT), flush=True)
    async def bounded():
        async with asyncio.timeout(120): await run(directory, game)
    try: asyncio.run(bounded())
    finally:
        for name in ('relay.json','host.json','consumer.json','key.pem'):
            with contextlib.suppress(FileNotFoundError): (directory/name).unlink()

if __name__ == '__main__': main()
