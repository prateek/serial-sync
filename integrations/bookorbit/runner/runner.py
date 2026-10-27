#!/usr/bin/env python3
"""BookOrbit runner protocol v1 for a serial-sync systemd deployment."""
import argparse
import datetime as dt
import hmac
import json
import os
import re
import sqlite3
import threading
import subprocess
import sys
import time
import tomllib
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

from serial_adapter import Systemd
from execution import RunLock, execute, journal
from store import Busy, Store


class Bridge:
    def __init__(self, store, manager, lock_path, schedule='Managed externally'):
        self.store, self.manager, self.schedule = store, manager, schedule
        self.lock_path = lock_path

    def reconcile(self):
        candidates = []
        for run in self.store.active_runs():
            age = (dt.datetime.now(dt.timezone.utc) - dt.datetime.fromisoformat(run['started_at'])).total_seconds()
            if run['state'] == 'running' or (age > 120 and not self.manager.request_active(run['request_key'])):
                candidates.append(run)
        if not candidates:
            return
        with RunLock(self.lock_path) as lock:
            if lock is None:
                return
        # Run IDs never restart. A queued row claimed since the probe must survive this update.
        # Keep systemd and SQLite waits outside the execution lock so reads cannot skip sync ticks.
        for run in candidates:
            self.store.finish(run['id'], 'interrupted', 'Execution ended without a completion receipt. Inspect the service journal.', expected_state=run['state'])

    def describe(self):
        return {'protocolVersion': 1, 'instanceId': self.store.instance(), 'name': 'Serial Sync', 'jobs': [{'id': 'sync', 'label': 'Sync subscribed serials', 'canStart': True, 'canCancel': False, 'schedule': self.schedule}]}

    def start(self, key):
        existing = self.store.by_key(key)
        if existing:
            return existing
        self.reconcile()
        if self.manager.active():
            raise Busy()
        with RunLock(self.lock_path) as lock:
            if lock is None:
                raise Busy()
        run, created = self.store.reserve(key)
        if created:
            # A failed RPC can have been accepted by systemd. Keep the reservation until reconciliation.
            self.manager.start(key)
        return self.store.get(run['id'])


def handler(bridge, token):
    class Handler(BaseHTTPRequestHandler):
        def setup(self):
            super().setup()
            self.connection.settimeout(10)

        def log_message(self, *_args):
            pass

        def reply(self, status, body):
            data = json.dumps(body).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(data)))
            self.send_header('Cache-Control', 'no-store')
            self.end_headers()
            self.wfile.write(data)

        def authorized(self):
            return hmac.compare_digest(self.headers.get('Authorization', '').encode('utf-8'), ('Bearer ' + token).encode('ascii'))

        def do_GET(self):
            if not self.authorized():
                return self.reply(401, {'error': 'unauthorized'})
            try:
                url = urlsplit(self.path)
                params = parse_qs(url.query, strict_parsing=True)
                if set(params) - {'limit', 'cursor'} or any(len(v) != 1 for v in params.values()):
                    raise ValueError()
                limit = int(params.get('limit', ['25'])[0])
                cursor = params.get('cursor', [None])[0]
                if not 1 <= limit <= 100 or (cursor is not None and (not cursor.isdigit() or len(cursor) > 18)):
                    raise ValueError()
                if url.path == '/capabilities':
                    return self.reply(200, bridge.describe())
                bridge.reconcile()
                if url.path == '/runs':
                    return self.reply(200, bridge.store.page(limit, cursor))
                match = re.fullmatch(r'/runs/(exec_[a-f0-9]{32})(/events)?', url.path)
                if match:
                    run = bridge.store.get(match[1])
                    if run:
                        return self.reply(200, bridge.store.events(match[1], limit, cursor) if match[2] else run)
                return self.reply(404, {'error': 'not found'})
            except ValueError:
                return self.reply(400, {'error': 'invalid query'})
            except (OSError, subprocess.SubprocessError, sqlite3.Error):
                return self.reply(503, {'error': 'service status unavailable'})

        def do_POST(self):
            if not self.authorized():
                self.close_connection = True
                return self.reply(401, {'error': 'unauthorized'})
            try:
                size = int(self.headers.get('Content-Length', '0'))
                if self.headers.get('Transfer-Encoding') or not 0 < size <= 4096:
                    self.close_connection = True
                    return self.reply(413, {'error': 'invalid body size'})
                body = json.loads(self.rfile.read(size))
                if self.path != '/runs':
                    return self.reply(404, {'error': 'unsupported operation'})
                if not isinstance(body, dict) or set(body) != {'jobId', 'idempotencyKey'} or body['jobId'] != 'sync':
                    raise ValueError()
                key = str(uuid.UUID(body['idempotencyKey']))
                return self.reply(202, bridge.start(key))
            except (ValueError, TypeError, AttributeError):
                return self.reply(400, {'error': 'invalid execution request'})
            except Busy:
                return self.reply(409, {'error': 'busy'})
            except (OSError, subprocess.SubprocessError, sqlite3.Error):
                return self.reply(503, {'error': 'service start unavailable'})
    return Handler


class BoundedHTTPServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, *args, reconcile=None):
        self.slots = threading.BoundedSemaphore(16)
        self.reconcile = reconcile
        self.next_reconcile = 0
        super().__init__(*args)

    def service_actions(self):
        if self.reconcile is None or time.monotonic() < self.next_reconcile:
            return
        self.next_reconcile = time.monotonic() + 30
        try:
            self.reconcile()
        except (OSError, subprocess.SubprocessError, sqlite3.Error):
            print('[runner.reconcile] [fail] errorClass=StatusUnavailable error="service status unavailable" - retaining last known state', file=sys.stderr)

    def process_request(self, request, address):
        if not self.slots.acquire(blocking=False):
            self.shutdown_request(request)
            return
        try:
            super().process_request(request, address)
        except BaseException:
            self.slots.release()
            raise

    def process_request_thread(self, request, address):
        try:
            super().process_request_thread(request, address)
        finally:
            self.slots.release()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    parser.add_argument('mode', choices=['serve', 'execute'])
    parser.add_argument('--origin', choices=['schedule', 'external', 'manual'], default='schedule')
    parser.add_argument('--request-key')
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    cfg = tomllib.loads(Path(args.config).read_text())
    os.umask(0o077)
    state = Path(cfg['state_dir'])
    def open_store():
        state.mkdir(parents=True, exist_ok=True)
        return Store(state / 'runner.db')
    store = journal(open_store) if args.mode == 'execute' and args.origin != 'manual' else open_store()
    if args.mode == 'execute':
        command = args.command[1:] if args.command[:1] == ['--'] else args.command
        if not command:
            parser.error('execute requires a fixed command after --')
        return execute(store, command, cfg['catalog'], cfg['logs'], args.origin, cfg['lock_path'], args.request_key)
    token = Path(cfg['token_file']).read_text().strip()
    if not re.fullmatch(r'[\x21-\x7e]{32,4096}', token):
        raise ValueError('Token file must contain at least 32 printable ASCII characters')
    bridge = Bridge(store, Systemd(cfg['unit'], cfg['manual_unit']), cfg['lock_path'], cfg.get('schedule', 'Managed externally'))
    server = BoundedHTTPServer((cfg.get('listen_host', '127.0.0.1'), cfg.get('listen_port', 8091)), handler(bridge, token), reconcile=bridge.reconcile)
    server.timeout = 30
    server.serve_forever()


if __name__ == '__main__':
    sys.exit(main())
