#!/usr/bin/env python3
import argparse
import asyncio
import contextlib
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import secrets
import socket
import ssl
import struct
import tempfile
import time
import uuid

VERSION = '26.10.31'
BINARY_SHA256 = 'cc4b84d3069569f297a61f04b914f2bb37e9d202fd7b84cb50def1fc30fd40e1'
VIRTUAL = '192.0.2.77'
TCP_PORT, UDP_PORT = 25565, 27960


class Engine:
    def __init__(self, executable, directory, name, config):
        self.executable, self.directory, self.name = executable, directory, name
        self.config = config
        self.events = asyncio.Queue(maxsize=256)
        self.process = None
        self.reader = None

    async def start(self):
        path = self.directory / (self.name + '.json')
        with open(path, 'x', opener=lambda p, flags: os.open(p, flags, 0o600)) as f:
            json.dump(self.config, f)
        self.process = await asyncio.create_subprocess_exec(
            str(self.executable), 'run', '-c', str(path),
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
        self.transport.sendto(data, addr)


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


async def experiment(executable, directory):
    engines, tasks, writers = [], set(), set()
    tcp_writers, consumers = set(), []
    servers, transports = [], []
    tcp_received = 0

    async def echo(reader, writer):
        nonlocal tcp_received
        tcp_writers.add(writer)
        tasks.add(asyncio.current_task())
        writers.add(writer)
        try:
            while data := await reader.read(65536):
                tcp_received += 1
                writer.write(data)
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
        host = Engine(executable, directory, 'host', host_config)
        engines.append(host)
        await host.start()
        await relay.event('v1.rvs.cool', 10)
        await asyncio.gather(*[
            exchange(consumer_port, client_tls, who, VIRTUAL, TCP_PORT,
                     ('tcp-' + str(i)).encode())
            for i, who in enumerate((consumer1, consumer2))])
        print('PASS: Reality native reverse; two authenticated TCP consumers')

        async def udp_consumer(who, identity):
            reader, writer = await request(consumer_port, client_tls, who, VIRTUAL, UDP_PORT, True)
            try:
                payloads = [bytes([identity]) * n for n in (1, 73, 1200, 4096)]
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
        print('PASS: 128 concurrent UDP datagrams, 1/73/1200/4096 bytes, independent reply paths')
        before_tcp, before_udp = tcp_received, udp_echo.received
        await asyncio.gather(*[
            rejected(consumer_port, client_tls, consumer1, address, port, udp=udp)
            for address, port, udp in [
                (VIRTUAL, UDP_PORT, False), (VIRTUAL, TCP_PORT, True),
                (VIRTUAL, TCP_PORT + 1, False), ('192.0.2.78', TCP_PORT, False),
                ('127.0.0.1', tcp_local, False), ('127.0.0.1', udp_local, True),
                ('10.0.0.1', TCP_PORT, False), ('localhost', TCP_PORT, False)]],
            rejected(consumer_port, client_tls, uuid.uuid4(), VIRTUAL, TCP_PORT))
        assert (tcp_received, udp_echo.received) == (before_tcp, before_udp), 'denied target received data'
        print('PASS: unauthorized UUID, wrong address/port/protocol, loopback/LAN/domain destinations denied')
        async def hold_connection():
            reader, writer = await request(consumer_port, client_tls, consumer1, VIRTUAL, TCP_PORT)
            consumers.append((reader, writer))
            writer.write(b'held')
            await writer.drain()
            await response(reader)
            assert await reader.readexactly(4) == b'held', 'held connection failed'

        await asyncio.gather(*(hold_connection() for _ in range(257)))
        assert len(tcp_writers) == 257, 'expected 257 simultaneous loopback target connections'
        print('BLOCKER: unmodified publisher admits 257 active TCP sessions, exceeding the 256 budget')
        await host.stop()
        for reader, writer in consumers:
            assert await reader.read(1) == b'', 'publisher stop left a forwarded session alive'
            writer.close()
            with contextlib.suppress(ConnectionError):
                await writer.wait_closed()
        consumers.clear()
        print('PASS: stopping publisher closes all 257 forwarded TCP sessions')
        invalid_publisher = uuid.uuid4()
        host_config['outbounds'][-1]['settings']['id'] = str(invalid_publisher)
        failed_host = Engine(executable, directory, 'invalid-credential-host', host_config)
        engines.append(failed_host)
        await failed_host.start()
        attempts = []
        for _ in range(3):
            await failed_host.event('tunneling request to', 12)
            attempts.append(await relay.event('proxy/vless/inbound: invalid request from ',
                                             5, also='invalid request user id: ' + str(invalid_publisher)))
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
    args = parser.parse_args()
    executable = args.xray.resolve(strict=True)
    if hashlib.sha256(executable.read_bytes()).hexdigest() != BINARY_SHA256:
        parser.error('expected the pinned Linux amd64 Xray ' + VERSION + ' release binary')
    os.umask(0o077)
    scratch = Path(__file__).resolve().parents[2] / 'dist' / 'hosting'
    scratch.mkdir(parents=True, exist_ok=True, mode=0o700)
    with tempfile.TemporaryDirectory(prefix='experiment-', dir=scratch) as work:
        async def run():
            async with asyncio.timeout(60):
                await experiment(executable, Path(work))
        asyncio.run(run())


if __name__ == '__main__':
    main()
