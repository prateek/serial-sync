import concurrent.futures
import contextlib
import io
import json
import sqlite3
import subprocess
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
import uuid
from http.server import ThreadingHTTPServer
from pathlib import Path
from unittest.mock import Mock, patch

from runner import BoundedHTTPServer, Bridge, execute, handler
from execution import RunLock
from store import Busy, Store

TOKEN = 'fixture-token-' + 'x' * 32


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.store = Store(self.root / 'runner.db')
        self.manager = Mock()
        self.manager.active.return_value = False
        self.manager.request_active.return_value = True
        self.lock_path = self.root / 'run.lock'
        self.bridge = Bridge(self.store, self.manager, self.lock_path)

    def test_idempotency_survives_restart_and_does_not_restart_completed_work(self):
        key = str(uuid.uuid4())
        run = self.bridge.start(key)
        self.assertEqual(self.bridge.start(key)['id'], run['id'])
        self.manager.start.assert_called_once()
        self.store.finish(run['id'], 'succeeded', 'Done')
        restored = Bridge(Store(self.root / 'runner.db'), self.manager, self.lock_path)
        self.assertEqual(restored.start(key)['state'], 'succeeded')
        self.manager.start.assert_called_once()

    def test_concurrent_requests_reserve_only_one_execution(self):
        def reserve(_):
            try:
                return self.store.reserve(str(uuid.uuid4()))[1]
            except Busy:
                return False
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            self.assertEqual(sum(pool.map(reserve, range(8))), 1)

    def test_scheduled_and_manual_executions_share_history(self):
        first = self.store.claim('schedule')
        self.store.finish(first, 'succeeded', 'Done')
        requested_key = str(uuid.uuid4())
        requested = self.bridge.start(requested_key)
        claimed = self.store.claim('manual', requested_key)
        self.assertEqual(claimed, requested['id'])
        self.assertEqual(self.store.get(claimed)['origin'], 'manual')
        page = self.store.page(1)
        self.assertEqual(page['items'][0]['id'], claimed)
        self.assertEqual(self.store.page(1, page['nextCursor'])['items'][0]['id'], first)

    def test_start_failure_is_durable(self):
        self.manager.start.side_effect = subprocess.CalledProcessError(1, ['systemctl'])
        with self.assertRaises(subprocess.SubprocessError):
            self.bridge.start(str(uuid.uuid4()))
        self.assertEqual(self.store.page()['items'][0]['state'], 'queued')

    def test_stale_execution_is_interrupted_not_succeeded(self):
        run_id = self.store.claim('schedule')
        with self.store.connect() as db:
            db.execute("UPDATE runs SET started_at='2020-01-01T00:00:00+00:00'")
        self.manager.active.return_value = False
        self.bridge.reconcile()
        self.assertEqual(self.store.get(run_id)['state'], 'interrupted')

    def test_idle_server_reconciles_without_a_browser_request(self):
        run_id = self.store.claim('schedule')
        with self.store.connect() as db:
            db.execute("UPDATE runs SET started_at='2020-01-01T00:00:00+00:00'")
        self.manager.active.return_value = False
        server = BoundedHTTPServer(('127.0.0.1', 0), handler(self.bridge, TOKEN), reconcile=self.bridge.reconcile)
        self.addCleanup(server.server_close)
        server.service_actions()
        self.assertEqual(self.store.get(run_id)['state'], 'interrupted')
        self.assertIsNotNone(self.store.claim('schedule'))

    def test_wrapper_collects_explicit_child_receipts_and_held_counts(self):
        catalog = self.root / 'state.db'
        with sqlite3.connect(catalog) as db:
            db.execute('CREATE TABLE run_records(id TEXT, status TEXT)')
            db.executemany('INSERT INTO run_records VALUES (?,?)', [('run_sync', 'succeeded'), ('run_publish', 'succeeded')])
        (self.root / 'run_sync.jsonl').write_text(json.dumps({'kind': 'release_synced'}) + '\n')
        (self.root / 'run_publish.jsonl').write_text('\n'.join(json.dumps({'kind': kind}) for kind in ['publish_held', 'publish_held', 'publish_skipped', 'publish_completed']))
        with contextlib.redirect_stdout(io.StringIO()):
            code = execute(self.store, [sys.executable, '-c', "print('  run_id: run_sync');print('  run_id: run_publish')"], catalog, self.root, 'schedule', self.lock_path)
        self.assertEqual(code, 0)
        run = self.store.page()['items'][0]
        self.assertEqual(run['childRunIds'], ['run_sync', 'run_publish'])
        self.assertEqual(run['counters'], {'syncedEvents': 1, 'heldEvents': 2, 'unchangedEvents': 1, 'publishedEvents': 1})
        self.assertEqual(run['importStatus'], 'unconfirmed')

    def test_lock_skip_does_not_report_sync_success(self):
        code = execute(self.store, [sys.executable, '-c', 'raise SystemExit(75)'], '', '', 'external', self.lock_path)
        self.assertEqual(code, 75)
        self.assertEqual(self.store.page()['items'][0]['state'], 'busy')

    def test_stale_journal_does_not_block_scheduled_execution_without_bridge(self):
        stale = self.store.claim('schedule')
        marker = self.root / 'ran'
        code = execute(self.store, [sys.executable, '-c', f"from pathlib import Path; Path({str(marker)!r}).touch()"], '', '', 'schedule', self.lock_path)
        self.assertEqual(code, 0)
        self.assertTrue(marker.exists())
        self.assertEqual(self.store.get(stale)['state'], 'interrupted')

    def test_unavailable_journal_does_not_block_scheduled_execution(self):
        self.store.claim = Mock(side_effect=sqlite3.OperationalError('locked'))
        marker = self.root / 'ran'
        code = execute(self.store, [sys.executable, '-c', f"from pathlib import Path; Path({str(marker)!r}).touch()"], '', '', 'schedule', self.lock_path)
        self.assertEqual(code, 0)
        self.assertTrue(marker.exists())

    def test_live_external_execution_survives_reconcile(self):
        run_id = self.store.claim('external')
        with self.store.connect() as db:
            db.execute("UPDATE runs SET started_at='2020-01-01T00:00:00+00:00'")
        with RunLock(self.lock_path):
            self.bridge.reconcile()
        self.assertEqual(self.store.get(run_id)['state'], 'running')
        self.store.finish(run_id, 'succeeded', 'Done')
        self.assertEqual(self.store.get(run_id)['state'], 'succeeded')

    def test_timer_does_not_claim_a_pending_manual_request(self):
        key = str(uuid.uuid4())
        manual = self.bridge.start(key)
        scheduled = self.store.claim('schedule')
        self.assertNotEqual(scheduled, manual['id'])
        self.assertEqual(self.store.get(scheduled)['origin'], 'schedule')
        self.assertEqual(self.store.get(manual['id'])['state'], 'queued')
        self.store.finish(scheduled, 'succeeded', 'Done')
        claimed = self.store.claim('manual', key)
        self.assertEqual(claimed, manual['id'])
        self.store.finish(claimed, 'succeeded', 'Done')
        self.assertIsNone(self.store.claim('manual', key))

    def test_manual_request_uses_a_distinct_service_instance_and_refuses_active_timer(self):
        self.manager.active.return_value = True
        with self.assertRaises(Busy):
            self.bridge.start(str(uuid.uuid4()))
        self.manager.active.return_value = False
        key = str(uuid.uuid4())
        self.bridge.start(key)
        self.manager.start.assert_called_once_with(key)

    def test_real_lock_contention_records_busy_without_starting_command(self):
        with RunLock(self.lock_path):
            code = execute(self.store, [sys.executable, '-c', 'raise SystemExit(99)'], '', '', 'schedule', self.lock_path)
        self.assertEqual(code, 75)
        self.assertEqual(self.store.page()['items'][0]['state'], 'busy')

    def test_transient_monitor_probe_does_not_skip_execution(self):
        marker = self.root / 'ran'
        with RunLock(self.lock_path) as probe:
            with patch('execution.time.sleep', side_effect=lambda _: probe.__exit__()):
                code = execute(self.store, [sys.executable, '-c', f"from pathlib import Path; Path({str(marker)!r}).touch()"], '', '', 'schedule', self.lock_path)
        self.assertEqual(code, 0)
        self.assertTrue(marker.exists())

    def test_reconcile_does_not_interrupt_a_queued_request_claimed_after_probe(self):
        key = str(uuid.uuid4())
        run = self.bridge.start(key)
        with self.store.connect() as db:
            db.execute("UPDATE runs SET started_at='2020-01-01T00:00:00+00:00'")
        self.manager.request_active.return_value = False
        finish = self.store.finish
        def claim_then_finish(*args, **kwargs):
            with RunLock(self.lock_path):
                self.store.claim('manual', key)
            finish(*args, **kwargs)
        self.store.finish = claim_then_finish
        self.bridge.reconcile()
        self.assertEqual(self.store.get(run['id'])['state'], 'running')

    def test_http_auth_validation_capabilities_and_pagination(self):
        server = ThreadingHTTPServer(('127.0.0.1', 0), handler(self.bridge, TOKEN))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        base = f'http://127.0.0.1:{server.server_port}'
        def request(path, body=None, token=TOKEN):
            req = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None, headers={'Authorization': 'Bearer ' + token})
            try:
                with urllib.request.urlopen(req) as response:
                    return response.status, json.load(response)
            except urllib.error.HTTPError as error:
                with error:
                    return error.code, json.load(error)
        self.assertEqual(request('/runs', token='wrong')[0], 401)
        self.assertEqual(request('/runs', token='caf\u00e9')[0], 401)
        self.assertFalse(request('/capabilities')[1]['jobs'][0]['canCancel'])
        self.assertEqual(request('/runs?limit=100000')[0], 400)
        self.assertEqual(request('/runs', {'jobId': 'sync', 'idempotencyKey': str(uuid.uuid4()), 'command': 'arbitrary'})[0], 400)
        key = str(uuid.uuid4())
        status, run = request('/runs', {'jobId': 'sync', 'idempotencyKey': key})
        self.assertEqual(status, 202)
        self.assertEqual(request('/runs', {'jobId': 'sync', 'idempotencyKey': key})[1]['id'], run['id'])
        self.assertEqual(request('/runs', {'jobId': 'sync', 'idempotencyKey': str(uuid.uuid4())})[0], 409)
        self.assertEqual(request('/runs/' + run['id'])[1]['state'], 'queued')
        self.assertEqual(request('/runs/' + run['id'] + '/cancel', {'idempotencyKey': str(uuid.uuid4())})[0], 404)
        self.store.page = Mock(side_effect=sqlite3.OperationalError('locked'))
        self.assertEqual(request('/runs')[0], 503)


if __name__ == '__main__':
    unittest.main()
