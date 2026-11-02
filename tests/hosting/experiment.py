#!/usr/bin/env python3
import argparse
import asyncio
import contextlib
import copy
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import secrets
import signal
import statistics
import socket
import ssl
import struct
import tempfile
import time
import uuid

VERSION = '26.10.31'
BINARY_SHA256 = 'cc4b84d3069569f297a61f04b914f2bb37e9d202fd7b84cb50def1fc30fd40e1'
VIRTUAL = '10.254.198.31'
TCP_PORT, UDP_PORT = 25565, 27960


class Engine:
    def __init__(self, executable, directory, name, config, adapter=False):
        self.executable, self.directory, self.name = executable, directory, name
        self.config = config
        self.adapter = adapter
        self.events = asyncio.Queue(maxsize=256)
        self.process = None
        self.reader = None
        self.sequence = 0

    async def start(self):
        path = self.directory / (self.name + '.json')
        with open(path, 'x', opener=lambda p, flags: os.open(p, flags, 0o600)) as f:
            json.dump(self.config, f)
        self.process = await asyncio.create_subprocess_exec(
            str(self.executable), *(['--config', str(path)] if self.adapter else ['run', '-c', str(path)]),
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.STDOUT,
            env={'PATH': '/usr/bin:/bin'}, limit=8192)
        self.reader = asyncio.create_task(self.read_events())
        await self.event('started', 10)

    async def read_events(self):
        try:
            while line := await self.process.stdout.readline():
                if self.events.full():
                    self.events.get_nowait()
                self.events.put_nowait((time.monotonic(), line.decode(errors='replace')))
        finally:
            if self.events.full():
                self.events.get_nowait()
            self.events.put_nowait((time.monotonic(), 'fixture: process exited'))

    async def event(self, text, timeout=10, also=''):
        async with asyncio.timeout(timeout):
            while True:
                stamp, line = await self.events.get()
                if text in line and also in line:
                    return stamp
                if 'fixture: process exited' in line:
                    raise RuntimeError(self.name + ' exited; raw engine output suppressed')

    async def apply(self, config=None, credential=None, running=True, retry=False):
        self.sequence += 1
        command = {'id': self.sequence, 'config': config or self.config['config'],
                   'credential': self.config['credential'] if credential is None else credential,
                   'running': running, 'retry': retry}
        self.process.stdin.write(json.dumps(command).encode() + b'\n')
        await self.process.stdin.drain()
        async with asyncio.timeout(12):
            while True:
                _, line = await self.events.get()
                if line.startswith('ack '):
                    ack = json.loads(line[4:])
                    assert ack['id'] == self.sequence and ack['ok'], 'typed update rejected'
                    assert not command['credential'] or command['credential'] not in line, 'credential leaked in acknowledgement'
                    self.observed = ack
                    return ack['snapshot']
                if 'fixture: process exited' in line:
                    raise RuntimeError('publisher exited during update')

    async def stop(self):
        if self.process is not None and self.process.returncode is None:
            self.process.terminate()
            try:
                await asyncio.wait_for(self.process.wait(), 3)
            except TimeoutError:
                self.process.kill()
                await self.process.wait()
        if self.reader is not None:
            await self.reader


class DatagramEcho(asyncio.DatagramProtocol):
    def __init__(self):
        self.received = 0
        self.peers = set()

    def connection_made(self, transport):
        self.transport = transport

    def datagram_received(self, data, addr):
        self.received += 1
        self.peers.add(addr)
        self.transport.sendto(b'x' * 8193 if data == b'oversized-reply' else data, addr)


class EndpointDNS(asyncio.DatagramProtocol):
    def __init__(self):
        self.address = '127.0.0.1'
        self.queries = []

    def connection_made(self, transport):
        self.transport = transport

    def datagram_received(self, data, addr):
        if len(data) > 512 or len(data) < 17:
            return
        end = 12
        while end < len(data) and data[end]:
            end += data[end] + 1
        end += 1
        if end + 4 != len(data):
            return
        typ, cls = struct.unpack('!HH', data[end:end+4])
        self.queries.append((self.address, typ))
        ip = ipaddress.ip_address(self.address)
        answer = cls == 1 and typ == (1 if ip.version == 4 else 28)
        packet = data[:2] + struct.pack('!HHHHH', 0x8180, 1, int(answer), 0, 0) + data[12:]
        if answer:
            packet += b'\xc0\x0c' + struct.pack('!HHIH', typ, 1, 0, len(ip.packed)) + ip.packed
        self.transport.sendto(packet, addr)


def unused_ports():
    with contextlib.ExitStack() as stack:
        sockets = [stack.enter_context(socket.socket()) for _ in range(2)]
        for sock in sockets:
            sock.bind(('127.0.0.1', 0))
        return [sock.getsockname()[1] for sock in sockets]


def rule(tag, network, port):
    return {'type': 'field', 'inboundTag': ['hosting'], 'ip': [VIRTUAL + '/32'],
            'network': network, 'port': str(port), 'outboundTag': tag}


def target(tag, network, port):
    return {'tag': tag, 'protocol': 'freedom', 'settings': {
        'redirect': '127.0.0.1:' + str(port), 'finalRules': [
            {'action': 'allow', 'network': network, 'ip': '127.0.0.1', 'port': str(port)},
            {'action': 'block', 'blockDelay': '0'}]}}


async def command(*args):
    proc = await asyncio.create_subprocess_exec(*map(str, args),
        stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL,
        env={'PATH': '/usr/bin:/bin'})
    try:
        output, _ = await asyncio.wait_for(proc.communicate(), 10)
        if proc.returncode:
            raise RuntimeError('fixture prerequisite failed; output suppressed')
        return output.decode()
    finally:
        if proc.returncode is None:
            proc.kill()
            await proc.wait()


async def request(port, context, credential, address, destination_port, udp=False):
    reader, writer = await asyncio.open_connection('127.0.0.1', port,
        ssl=context, server_hostname='relay.example', ssl_handshake_timeout=3)
    try:
        try:
            parsed = ipaddress.ip_address(address)
            encoded = bytes([1 if parsed.version == 4 else 3]) + parsed.packed
        except ValueError:
            raw = address.encode('ascii')
            encoded = bytes([2, len(raw)]) + raw
        writer.write(b'\x00' + credential.bytes + bytes([0, 2 if udp else 1]) +
                     struct.pack('!H', destination_port) + encoded)
        await writer.drain()
        return reader, writer
    except BaseException:
        writer.close()
        await writer.wait_closed()
        raise


async def response(reader):
    header = await reader.readexactly(2)
    assert header[0] == 0, 'unexpected VLESS version'
    await reader.readexactly(header[1])


async def exchange(port, context, credential, address, destination_port, payload=b'echo', udp=False):
    reader, writer = await request(port, context, credential, address, destination_port, udp)
    try:
        writer.write((struct.pack('!H', len(payload)) if udp else b'') + payload)
        await writer.drain()
        await response(reader)
        if udp:
            length = struct.unpack('!H', await reader.readexactly(2))[0]
            assert length == len(payload), 'datagram boundary changed'
        assert await reader.readexactly(len(payload)) == payload, 'reply crossed consumers'
    finally:
        writer.close()
        with contextlib.suppress(ConnectionError):
            await writer.wait_closed()


async def rejected(*args, **kwargs):
    try:
        async with asyncio.timeout(2):
            await exchange(*args, **kwargs)
    except (TimeoutError, ConnectionError, asyncio.IncompleteReadError, ssl.SSLError):
        return
    raise AssertionError('forbidden traffic reached an echo target')


async def experiment(executable, directory, publisher_binary=None, performance=False):
    engines, tasks, writers = [], set(), set()
    tcp_writers, consumers = set(), []
    servers, transports = [], []
    tcp_received = 0

    async def echo(reader, writer, prefix=b''):
        nonlocal tcp_received
        tcp_writers.add(writer)
        tasks.add(asyncio.current_task())
        writers.add(writer)
        try:
            while data := await reader.read(65536):
                tcp_received += 1
                writer.write(prefix + data)
                await writer.drain()
        except ConnectionError:
            pass
        finally:
            writer.close()
            tcp_writers.discard(writer)
            writers.discard(writer)
            tasks.discard(asyncio.current_task())

    async def camouflage(reader, writer):
        tasks.add(asyncio.current_task())
        writers.add(writer)
        try:
            await reader.read(1024)
        except ConnectionError:
            pass
        finally:
            writer.close()
            writers.discard(writer)
            tasks.discard(asyncio.current_task())

    try:
        cert, key = directory / 'certificate.pem', directory / 'key.pem'
        await command('openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt',
            'ec_paramgen_curve:P-256', '-nodes', '-keyout', key, '-out', cert,
            '-days', '1', '-subj', '/CN=relay.example', '-addext', 'subjectAltName=DNS:relay.example')
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.minimum_version = ssl.TLSVersion.TLSv1_3
        tls.load_cert_chain(cert, key)
        tls.set_alpn_protocols(['h2', 'http/1.1'])
        client_tls = ssl.create_default_context(cafile=str(cert))
        cover = await asyncio.start_server(camouflage, '127.0.0.1', 0, ssl=tls)
        servers.append(cover)
        tcp = await asyncio.start_server(echo, '127.0.0.1', 0)
        servers.append(tcp)
        udp_echo = DatagramEcho()
        transport, _ = await asyncio.get_running_loop().create_datagram_endpoint(
            lambda: udp_echo, local_addr=('127.0.0.1', 0))
        transports.append(transport)
        tcp_local = tcp.sockets[0].getsockname()[1]
        udp_local = transport.get_extra_info('sockname')[1]
        reality_port, consumer_port = unused_ports()
        publisher, consumer1, consumer2 = uuid.uuid4(), uuid.uuid4(), uuid.uuid4()
        keys = dict(line.split(': ', 1) for line in (await command(executable, 'x25519')).splitlines())
        short_id = secrets.token_hex(8)
        stream = {'network': 'raw', 'security': 'reality', 'realitySettings': {
            'target': '127.0.0.1:' + str(cover.sockets[0].getsockname()[1]),
            'serverNames': ['relay.example'], 'privateKey': keys['PrivateKey'],
            'shortIds': [short_id]}}
        relay_config = {'log': {'loglevel': 'info'}, 'inbounds': [
            {'tag': 'publisher', 'listen': '127.0.0.1', 'port': reality_port,
             'protocol': 'vless', 'settings': {'decryption': 'none', 'clients': [
                {'id': str(publisher), 'reverse': {'tag': 'published'}}]}, 'streamSettings': stream},
            {'tag': 'consumers', 'listen': '127.0.0.1', 'port': consumer_port,
             'protocol': 'vless', 'settings': {'decryption': 'none', 'clients': [
                {'id': str(consumer1)}, {'id': str(consumer2)}]}, 'streamSettings': {
                    'network': 'raw', 'security': 'tls', 'tlsSettings': {'certificates': [
                        {'certificateFile': str(cert), 'keyFile': str(key)}]}}}],
            'outbounds': [{'tag': 'reject', 'protocol': 'blackhole'}],
            'routing': {'domainStrategy': 'AsIs', 'rules': [
                {'type': 'field', 'inboundTag': ['consumers'], 'outboundTag': 'published'}]}}
        host_config = {'log': {'loglevel': 'info'}, 'outbounds': [
            {'tag': 'reject', 'protocol': 'blackhole'}, target('tcp', 'tcp', tcp_local),
            target('udp', 'udp', udp_local),
            {'tag': 'relay', 'protocol': 'vless', 'settings': {
                'address': '127.0.0.1', 'port': reality_port, 'id': str(publisher),
                'encryption': 'none', 'reverse': {'tag': 'hosting'}},
             'streamSettings': {'network': 'raw', 'security': 'reality', 'realitySettings': {
                'serverName': 'relay.example', 'fingerprint': 'chrome',
                'password': keys['Password (PublicKey)'], 'shortId': short_id}}}],
            'routing': {'domainStrategy': 'AsIs', 'rules': [
                rule('tcp', 'tcp', TCP_PORT), rule('udp', 'udp', UDP_PORT)]}}
        relay = Engine(executable, directory, 'relay', relay_config)
        engines.append(relay)
        await relay.start()
        if publisher_binary:
            dns_fixture = EndpointDNS()
            dns_transport, _ = await asyncio.get_running_loop().create_datagram_endpoint(
                lambda: dns_fixture, local_addr=('127.0.0.1', 0))
            transports.append(dns_transport)
            host_config = {'dns': '127.0.0.1:' + str(dns_transport.get_extra_info('sockname')[1]), 'config': {'relay': {
                'version': 1, 'label': 'Fixture relay', 'host': 'endpoint.example',
                'port': reality_port, 'address': VIRTUAL,
                'reality': {'server_name': 'relay.example',
                            'public_key': keys['Password (PublicKey)'], 'short_id': short_id}},
                'shares': [
                    {'id': 'tcp', 'label': 'TCP echo', 'local_address': '127.0.0.1',
                     'local_port': tcp_local, 'exposed_port': TCP_PORT, 'protocol': 'tcp', 'enabled': True},
                    {'id': 'udp', 'label': 'UDP echo', 'local_address': '127.0.0.1',
                     'local_port': udp_local, 'exposed_port': UDP_PORT, 'protocol': 'udp', 'enabled': True}]},
                'credential': str(publisher)}
        host = Engine(publisher_binary or executable, directory, 'host', host_config, bool(publisher_binary))
        engines.append(host)
        await host.start()
        await relay.event('v1.rvs.cool', 10)
        await asyncio.gather(*[
            exchange(consumer_port, client_tls, who, VIRTUAL, TCP_PORT,
                     ('tcp-' + str(i)).encode())
            for i, who in enumerate((consumer1, consumer2))])
        print('PASS: Reality native reverse; two authenticated TCP consumers')

        if performance:
            async def load():
                while True:
                    await exchange(consumer_port, client_tls, consumer2, VIRTUAL, TCP_PORT, b'x' * 65536)
            load_task = asyncio.create_task(load())
            samples, lost, verified = [], 0, 0
            sizes = (1, 73, 1200, 4096, 8190)
            by_size = {n: {'verified': 0, 'lost': 0, 'times': []} for n in sizes}
            async def measure(who, identity, size, sequence):
                nonlocal lost, verified
                payload = bytes([identity]) + (struct.pack('!I', sequence) + secrets.token_bytes(size))[:size-1]
                start = time.monotonic()
                try:
                    async with asyncio.timeout(2):
                        await exchange(consumer_port, client_tls, who, VIRTUAL, UDP_PORT, payload, True)
                    elapsed = 1000 * (time.monotonic() - start)
                    samples.append(elapsed)
                    by_size[size]['times'].append(elapsed)
                    by_size[size]['verified'] += 1
                    verified += 1
                except (TimeoutError, ConnectionError, asyncio.IncompleteReadError):
                    by_size[size]['lost'] += 1
                    lost += 1
            try:
                for i in range(100):
                    size = sizes[i % len(sizes)]
                    await asyncio.gather(measure(consumer1, 1, size, i), measure(consumer2, 2, size, i))
                if load_task.done():
                    load_task.result()
                assert samples, 'no verified performance datagrams'
                for result in by_size.values():
                    times = result.pop('times')
                    result['median_ms'] = statistics.median(times) if times else None
                    result['p95_ms'] = sorted(times)[int(.95 * (len(times)-1))] if times else None
                print('MEASURE:', json.dumps({'samples': 200, 'verified_datagrams': verified,
                    'lost': lost, 'payload_bytes': sizes, 'independent_consumers': 2,
                    'by_payload_bytes': by_size, 'tcp_load_bytes_per_exchange': 65536,
                    'roundtrip_including_session_ms_median': statistics.median(samples),
                    'roundtrip_including_session_ms_p95': sorted(samples)[int(.95 * (len(samples)-1))],
                    'successive_rtt_jitter_ms': statistics.mean(abs(b-a) for a,b in zip(samples,samples[1:])) if len(samples)>1 else 0}))
            finally:
                load_task.cancel()
                await asyncio.gather(load_task, return_exceptions=True)

            return

        async def udp_consumer(who, identity):
            reader, writer = await request(consumer_port, client_tls, who, VIRTUAL, UDP_PORT, True)
            try:
                payloads = [bytes([identity]) * n for n in (1, 73, 1200, 4096, 8190)]
                for _ in range(16):
                    for payload in payloads:
                        writer.write(struct.pack('!H', len(payload)) + payload)
                    await writer.drain()
                    if _ == 0:
                        await response(reader)
                    for payload in payloads:
                        size = struct.unpack('!H', await reader.readexactly(2))[0]
                        assert size == len(payload), 'UDP datagram boundary changed'
                        assert await reader.readexactly(size) == payload, 'UDP consumer isolation failed'
            finally:
                writer.close()
                with contextlib.suppress(ConnectionError):
                    await writer.wait_closed()

        await asyncio.gather(udp_consumer(consumer1, 1), udp_consumer(consumer2, 2))
        assert len(udp_echo.peers) == 2, 'UDP consumers shared a local association'
        print('PASS: 160 concurrent UDP datagrams, 1/73/1200/4096/8190 bytes, independent reply paths')
        before_tcp, before_udp = tcp_received, udp_echo.received
        await asyncio.gather(*[
            rejected(consumer_port, client_tls, consumer1, address, port, udp=udp)
            for address, port, udp in [
                (VIRTUAL, UDP_PORT, False), (VIRTUAL, TCP_PORT, True),
                (VIRTUAL, TCP_PORT + 1, False), ('10.254.198.32', TCP_PORT, False),
                ('127.0.0.1', tcp_local, False), ('127.0.0.1', udp_local, True),
                ('10.0.0.1', TCP_PORT, False), ('localhost', TCP_PORT, False)]],
            rejected(consumer_port, client_tls, uuid.uuid4(), VIRTUAL, TCP_PORT))
        assert (tcp_received, udp_echo.received) == (before_tcp, before_udp), 'denied target received data'
        print('PASS: unauthorized UUID, wrong address/port/protocol, loopback/LAN/domain destinations denied')
        if publisher_binary:
            async def live(port, udp=False):
                r, w = await request(consumer_port, client_tls, consumer1, VIRTUAL, port, udp)
                consumers.append((r, w))
                w.write((b'\x00\x04' if udp else b'') + b'live')
                await w.drain()
                await response(r)
                assert await r.readexactly(6 if udp else 4) == (b'\x00\x04' if udp else b'') + b'live'
                return r, w

            original = copy.deepcopy(host_config['config'])
            await host.apply()
            oversized_before = host.observed['snapshot']['oversized']
            await rejected(consumer_port, client_tls, consumer1, VIRTUAL, UDP_PORT,
                           payload=b'oversized-reply', udp=True)
            async with asyncio.timeout(3):
                while True:
                    snapshot = await host.apply()
                    if snapshot['oversized'] > oversized_before:
                        break
                    await asyncio.sleep(.05)
            await exchange(consumer_port, client_tls, consumer1, VIRTUAL, UDP_PORT, b'after-drop', True)
            print('PASS: actual UDP target oversized reply dropped and counted; subsequent datagrams recover')

            for action in ('disable', 'remove', 'target', 'pause', 'master', 'credential'):
                affected = await live(TCP_PORT)
                datagram = await live(UDP_PORT, True)
                cfg = copy.deepcopy(original)
                if action == 'disable':
                    for sh in cfg['shares']: sh['enabled'] = False
                elif action == 'remove':
                    cfg['shares'] = []
                elif action == 'target':
                    for sh in cfg['shares']: sh['local_port'] = unused_ports()[0]
                elif action == 'pause':
                    cfg['paused'] = True
                snapshot = await host.apply(cfg, running=action != 'master',
                    credential='' if action == 'credential' else None)
                for r, w in (affected, datagram):
                    try:
                        assert await asyncio.wait_for(r.read(1), 3) == b'', action + ' retained access'
                    except ConnectionError:
                        pass
                    w.close()
                    consumers.remove((r, w))
                assert snapshot['active'] == 0, action + ' retained sessions'
                await rejected(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
                await host.apply(original)
                async with asyncio.timeout(12):
                    while True:
                        try:
                            async with asyncio.timeout(1):
                                await exchange(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
                            break
                        except (TimeoutError, ConnectionError, asyncio.IncompleteReadError):
                            await asyncio.sleep(.05)
                print('PASS: established TCP/UDP revocation:', action)
            for family, endpoint in (('IPv6', '::1'), ('IPv4', '127.0.0.1')):
                await host.apply(original, running=False)
                assert host.observed['active_runs'] == 0, 'suspend retained transport run'
                runs = host.observed['runs']
                for _ in range(3):
                    await host.apply(original, running=False)
                assert host.observed['runs'] == runs, 'suspended apply started transport'
                await relay.stop()
                dns_fixture.address = endpoint
                relay_cfg = copy.deepcopy(relay_config)
                relay_cfg['inbounds'][0]['listen'] = endpoint
                relay = Engine(executable, directory, 'relay-' + family, relay_cfg)
                engines.append(relay)
                await relay.start()
                started = time.monotonic()
                await host.apply(original)
                await relay.event('v1.rvs.cool', 12)
                await exchange(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
                for _ in range(3):
                    await host.apply(original)
                assert host.observed['active_runs'] == 1 and host.observed['maximum_runs'] == 1, 'duplicate publisher runs'
                assert host.observed['runs'] == runs + 1, 'unchanged apply restarted publisher'
                assert (endpoint, 1) in dns_fixture.queries and (endpoint, 28) in dns_fixture.queries
                print('PASS: simulated suspend/recovery, DNS endpoint', family,
                      'in', round(time.monotonic() - started, 2), 's; exactly one publisher run')
            relay_config = relay_cfg
            late_port = unused_ports()[0]
            cfg = copy.deepcopy(original)
            cfg['shares'][0]['local_port'] = late_port
            await host.apply(cfg)
            await rejected(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
            for _ in range(2):
                late = await asyncio.start_server(echo, '127.0.0.1', late_port)
                servers.append(late)
                await exchange(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
                late.close()
                await late.wait_closed()
            await host.apply(original)
            print('PASS: enable before target starts; target restart without relay reload')
            alternate = await asyncio.start_server(lambda r, w: echo(r, w, b'alternate:'), '127.0.0.1', 0)
            servers.append(alternate)
            changed = copy.deepcopy(original)
            changed['shares'][0]['local_port'] = alternate.sockets[0].getsockname()[1]
            await host.apply(changed)
            r, w = await request(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
            try:
                w.write(b'target')
                await w.drain()
                await response(r)
                assert await r.readexactly(16) == b'alternate:target', 'target edit retained previous destination'
            finally:
                w.close()
                with contextlib.suppress(ConnectionError):
                    await w.wait_closed()
            await host.apply(original)
            print('PASS: target edit forwards subsequent traffic to the new loopback destination')
            await relay.stop()
            await host.event('hosting reconnecting', 12)
            restarted = Engine(executable, directory, 'relay-restarted', relay_config)
            engines.append(restarted)
            await restarted.start()
            await restarted.event('v1.rvs.cool', 15)
            await exchange(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
            relay = restarted
            print('PASS: relay loss and recovery reconnects without reapplying shares')
            await host.apply(original)
            before = host.observed['runs']
            host.process.send_signal(signal.SIGSTOP)
            try:
                await relay.stop()
                recovered = Engine(executable, directory, 'relay-after-suspend', relay_config)
                engines.append(recovered)
                await recovered.start()
            finally:
                host.process.send_signal(signal.SIGCONT)
            await recovered.event('v1.rvs.cool', 15)
            await exchange(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
            await host.apply(original)
            assert host.observed['runs'] == before + 1 and host.observed['maximum_runs'] == 1, 'resume duplicated publisher'
            relay = recovered
            print('PASS: stopped process resumes after relay replacement; no duplicate publisher')
            udp_sessions = [await live(UDP_PORT, True) for _ in range(256)]
            await rejected(consumer_port, client_tls, consumer2, VIRTUAL, UDP_PORT, udp=True)
            for r, w in udp_sessions:
                w.write(b'\x00\x04kept')
                await w.drain()
                assert await r.readexactly(6) == b'\x00\x04kept', 'UDP admission evicted a session'
            cfg = copy.deepcopy(original)
            cfg['shares'][1]['enabled'] = False
            snapshot = await host.apply(cfg)
            assert snapshot['active'] == 0, 'UDP revocation retained associations'
            for r, w in udp_sessions:
                w.close()
                consumers.remove((r, w))
            await host.apply(original)
            print('PASS: 257th UDP association refused; 256 existing reply paths preserved')
            unaffected = await live(TCP_PORT)
            cfg = copy.deepcopy(original)
            cfg['shares'][1]['enabled'] = False
            await host.apply(cfg)
            cfg['shares'][1]['label'] = 'Disabled edit'
            await host.apply(cfg)
            r, w = unaffected
            w.write(b'kept')
            await w.drain()
            assert await r.readexactly(4) == b'kept', 'unaffected TCP interrupted'
            w.close()
            consumers.remove(unaffected)
            await host.apply(original)
            print('PASS: unaffected TCP survives share disable and disabled edit')

        async def hold_connection():
            reader, writer = await request(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
            consumers.append((reader, writer))
            writer.write(b'held')
            await writer.drain()
            await response(reader)
            assert await reader.readexactly(4) == b'held', 'held connection failed'

        held_count = 256 if publisher_binary else 257
        await asyncio.gather(*(hold_connection() for _ in range(held_count)))
        assert len(tcp_writers) == held_count, 'unexpected simultaneous target connection count'
        if publisher_binary:
            await rejected(consumer_port, client_tls, consumer2, VIRTUAL, TCP_PORT)
            for reader, writer in consumers:
                writer.write(b'kept')
                await writer.drain()
                assert await reader.readexactly(4) == b'kept', 'excess session evicted an existing session'
            print('PASS: 257th TCP session refused; all 256 admitted sessions preserved')
        else:
            print('BLOCKER: unmodified publisher admits 257 active TCP sessions, exceeding the 256 budget')
        await host.stop()
        for reader, writer in consumers:
            assert await reader.read(1) == b'', 'publisher stop left a forwarded session alive'
            writer.close()
            with contextlib.suppress(ConnectionError):
                await writer.wait_closed()
        consumers.clear()
        print('PASS: stopping publisher closes all', held_count, 'forwarded TCP sessions')
        invalid_publisher = uuid.uuid4()
        if publisher_binary:
            host_config['credential'] = str(invalid_publisher)
        else:
            host_config['outbounds'][-1]['settings']['id'] = str(invalid_publisher)
        failed_host = Engine(publisher_binary or executable, directory, 'invalid-credential-host', host_config, bool(publisher_binary))
        engines.append(failed_host)
        await failed_host.start()
        rejection = 'proxy/vless/inbound: invalid request from '
        rejected_id = 'invalid request user id: ' + str(invalid_publisher)
        if publisher_binary:
            attempts = [await relay.event(rejection, 12, also=rejected_id) for _ in range(3)]
            intervals = [b - a for a, b in zip(attempts, attempts[1:])]
            assert 0.9 <= intervals[0] <= 2 and 1.9 <= intervals[1] <= 4, 'registration loss did not back off'
            await failed_host.apply(credential=str(publisher))
            await relay.event('v1.rvs.cool', 12)
            await exchange(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
            await failed_host.stop()
            print('PASS: untyped registration close backs off; credential replacement recovers')
            host_config['credential'] = str(publisher)
            bad_security = json.loads(json.dumps(host_config))
            original = bad_security['config']['relay']['reality']['short_id']
            bad_security['config']['relay']['reality']['short_id'] = ('00' if original[:2] != '00' else 'ff') + original[2:]
            denied = Engine(publisher_binary, directory, 'invalid-reality-host', bad_security, True)
            engines.append(denied)
            await denied.start()
            await denied.event('hosting error', 12)
            snapshot = await denied.apply()
            assert snapshot['state'] == 'error' and denied.observed['runs'] == 1, 'verified security failure did not latch'
            await denied.apply(retry=True)
            await denied.event('hosting error', 12)
            await denied.apply()
            assert denied.observed['runs'] == 2, 'explicit retry did not perform exactly one attempt'
            await denied.apply(config=host_config['config'])
            await relay.event('v1.rvs.cool', 12)
            await exchange(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
            print('PASS: Reality security rejection latches; retry and profile replacement recover')
            print('RESULT: embedded adapter passes native reverse, authorization, admission and credential gates')
        else:
            attempts = []
            for _ in range(3):
                await failed_host.event('tunneling request to', 12)
                attempts.append(await relay.event(rejection, 5, also=rejected_id))
            intervals = [round(b - a, 2) for a, b in zip(attempts, attempts[1:])]
            print('BLOCKER: rejected publisher credential automatically retried; intervals (s):', intervals)
            print('RESULT: interoperability demonstrated; unmodified helper fails retry and session-budget contracts')
    finally:
        for _, writer in consumers:
            writer.close()
        for engine in reversed(engines):
            await engine.stop()
        for server in servers:
            server.close()
            await server.wait_closed()
        for transport in transports:
            transport.close()
        for writer in list(writers):
            writer.close()
        for task in list(tasks):
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)


def main():
    parser = argparse.ArgumentParser(description='Pinned, disposable native VLESS reverse experiment')
    parser.add_argument('--xray', type=Path, required=True)
    parser.add_argument('--adapter', type=Path)
    parser.add_argument('--performance', action='store_true')
    args = parser.parse_args()
    executable = args.xray.resolve(strict=True)
    if hashlib.sha256(executable.read_bytes()).hexdigest() != BINARY_SHA256:
        parser.error('expected the pinned Linux amd64 Xray ' + VERSION + ' release binary')
    os.umask(0o077)
    scratch = Path(__file__).resolve().parents[2] / 'dist' / 'hosting'
    scratch.mkdir(parents=True, exist_ok=True, mode=0o700)
    with tempfile.TemporaryDirectory(prefix='experiment-', dir=scratch) as work:
        async def run():
            async with asyncio.timeout(180):
                await experiment(executable, Path(work), args.adapter.resolve(strict=True) if args.adapter else None, args.performance)
        asyncio.run(run())


if __name__ == '__main__':
    main()
