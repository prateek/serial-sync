"""Durable executions shared by the HTTP bridge and the systemd wrapper."""
import contextlib
import datetime as dt
import json
import sqlite3
import uuid


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


class Busy(Exception):
    pass


class Store:
    def __init__(self, path):
        self.path = path
        with self.connect() as db:
            db.executescript('''
                PRAGMA journal_mode=WAL;
                CREATE TABLE IF NOT EXISTS instance (id TEXT PRIMARY KEY);
                CREATE UNIQUE INDEX IF NOT EXISTS one_instance ON instance ((1));
                CREATE TABLE IF NOT EXISTS runs (
                  seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
                  request_key TEXT UNIQUE, origin TEXT NOT NULL, state TEXT NOT NULL,
                  started_at TEXT NOT NULL, finished_at TEXT, phase TEXT,
                  counters TEXT NOT NULL DEFAULT '{}', message TEXT,
                  children TEXT NOT NULL DEFAULT '[]');
                DROP INDEX IF EXISTS one_active_run;
                CREATE UNIQUE INDEX IF NOT EXISTS one_running_run ON runs ((1)) WHERE state='running';
                CREATE TABLE IF NOT EXISTS events (
                  seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL,
                  timestamp TEXT NOT NULL, level TEXT NOT NULL, message TEXT NOT NULL);
                CREATE INDEX IF NOT EXISTS run_events ON events(run_id, seq);
            ''')
            if not db.execute('SELECT id FROM instance LIMIT 1').fetchone():
                db.execute('INSERT OR IGNORE INTO instance VALUES (?)', ('runner_' + uuid.uuid4().hex,))

    @contextlib.contextmanager
    def connect(self):
        db = sqlite3.connect(self.path, timeout=10)
        db.row_factory = sqlite3.Row
        try:
            with db:
                yield db
        finally:
            db.close()

    def instance(self):
        with self.connect() as db:
            return db.execute('SELECT id FROM instance LIMIT 1').fetchone()[0]

    def reserve(self, key):
        with self.connect() as db:
            db.execute('BEGIN IMMEDIATE')
            existing = db.execute('SELECT * FROM runs WHERE request_key=?', (key,)).fetchone()
            if existing:
                return self.present(existing), False
            if db.execute("SELECT 1 FROM runs WHERE state IN ('queued','running')").fetchone():
                raise Busy()
            run_id = self.insert(db, 'manual', 'queued', key)
            return self.present(db.execute('SELECT * FROM runs WHERE id=?', (run_id,)).fetchone()), True

    def by_key(self, key):
        with self.connect() as db:
            row = db.execute('SELECT * FROM runs WHERE request_key=?', (key,)).fetchone()
            return self.present(row) if row else None

    def claim(self, origin, request_key=None):
        # Caller holds run.lock, so a previous running receipt cannot still own execution.
        with self.connect() as db:
            db.execute('BEGIN IMMEDIATE')
            for row in db.execute("SELECT id FROM runs WHERE state='running'").fetchall():
                self.complete(db, row['id'], 'interrupted', 'Previous execution lost the shared execution lock.')
            if origin == 'manual':
                row = db.execute("SELECT * FROM runs WHERE request_key=?", (request_key,)).fetchone()
                if not row or row['state'] != 'queued':
                    return None
                run_id = row['id']
            else:
                run_id = self.insert(db, origin, 'queued', None)
            db.execute("UPDATE runs SET state='running',phase='sync and publish' WHERE id=?", (run_id,))
            self.add_event(db, run_id, 'info', 'Execution started. Import confirmation is separate.')
            return run_id

    def lock_busy(self, origin, request_key=None):
        with self.connect() as db:
            db.execute('BEGIN IMMEDIATE')
            if origin == 'manual':
                row = db.execute("SELECT id FROM runs WHERE request_key=? AND state='queued'", (request_key,)).fetchone()
                if not row:
                    return
                run_id = row['id']
            else:
                run_id = self.insert(db, origin, 'queued', None)
            self.complete(db, run_id, 'busy', 'Shared execution lock was held; no sync ran.')

    def insert(self, db, origin, state, key):
        run_id = 'exec_' + uuid.uuid4().hex
        db.execute('INSERT INTO runs(id,request_key,origin,state,started_at) VALUES (?,?,?,?,?)', (run_id, key, origin, state, now()))
        return run_id

    def finish(self, run_id, state, message, counters=None, children=None, expected_state=None):
        with self.connect() as db:
            self.complete(db, run_id, state, message, counters, children, expected_state)

    def complete(self, db, run_id, state, message, counters=None, children=None, expected_state=None):
        changed = db.execute("UPDATE runs SET state=?,finished_at=?,phase=NULL,message=?,counters=?,children=? WHERE id=? AND state IN ('queued','running') AND (? IS NULL OR state=?)", (state, now(), message, json.dumps(counters or {}), json.dumps(children or []), run_id, expected_state, expected_state)).rowcount
        if changed:
            self.add_event(db, run_id, 'info' if state in ('succeeded', 'busy') else 'warning', message)

    def active_runs(self):
        with self.connect() as db:
            return [dict(row) for row in db.execute("SELECT id, request_key, state, started_at FROM runs WHERE state IN ('queued','running') LIMIT 100")]

    def add_event(self, db, run_id, level, message):
        db.execute('INSERT INTO events(run_id,timestamp,level,message) VALUES (?,?,?,?)', (run_id, now(), level, message))

    def active(self):
        with self.connect() as db:
            row = db.execute("SELECT * FROM runs WHERE state IN ('queued','running')").fetchone()
            return self.present(row) if row else None

    def get(self, run_id):
        with self.connect() as db:
            row = db.execute('SELECT * FROM runs WHERE id=?', (run_id,)).fetchone()
            return self.present(row) if row else None

    def page(self, limit=25, cursor=None):
        with self.connect() as db:
            rows = db.execute('SELECT * FROM runs WHERE seq < ? ORDER BY seq DESC LIMIT ?', (int(cursor) if cursor else 9223372036854775807, limit + 1)).fetchall()
            return {'items': [self.present(row) for row in rows[:limit]], 'nextCursor': str(rows[limit-1]['seq']) if len(rows) > limit else None}

    def events(self, run_id, limit=25, cursor=None):
        with self.connect() as db:
            rows = db.execute('SELECT * FROM events WHERE run_id=? AND seq>? ORDER BY seq LIMIT ?', (run_id, int(cursor or 0), limit + 1)).fetchall()
            items = [{'id': 'event_' + str(row['seq']), 'timestamp': row['timestamp'], 'level': row['level'], 'message': row['message']} for row in rows[:limit]]
            return {'items': items, 'nextCursor': str(rows[limit-1]['seq']) if len(rows) > limit else None}

    def present(self, row):
        return {'id': row['id'], 'jobId': 'sync', 'origin': row['origin'], 'state': row['state'], 'startedAt': row['started_at'], 'finishedAt': row['finished_at'], 'phase': row['phase'], 'counters': json.loads(row['counters']), 'message': row['message'], 'childRunIds': json.loads(row['children']), 'importStatus': 'unconfirmed'}
