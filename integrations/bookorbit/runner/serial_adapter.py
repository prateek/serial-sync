"""Translate serial-sync results without exposing captured content or credentials."""
import collections
import json
import re
import sqlite3
import subprocess
import uuid
from pathlib import Path

RUN_ID = re.compile(r'^\s*run_id: (run_[a-zA-Z0-9_-]+)\s*$')
COUNTERS = {'publish_held': 'heldEvents', 'publish_skipped': 'unchangedEvents', 'publish_completed': 'publishedEvents', 'publish_failed': 'publishFailureEvents', 'release_synced': 'syncedEvents', 'release_unchanged': 'unchangedReleaseEvents'}


def summarize(catalog, logs, children):
    counts = collections.Counter()
    verified = []
    with sqlite3.connect(Path(catalog).resolve().as_uri() + '?mode=ro', uri=True) as db:
        for run_id in dict.fromkeys(children):
            row = db.execute('SELECT status FROM run_records WHERE id=?', (run_id,)).fetchone()
            if not row:
                continue
            verified.append(run_id)
            path = Path(logs) / (run_id + '.jsonl')
            try:
                with path.open() as stream:
                    while line := stream.readline(65537):
                        if len(line) > 65536:
                            raise ValueError('Oversized event')
                        event = json.loads(line)
                        if not isinstance(event, dict):
                            raise ValueError('Invalid event')
                        if event.get('kind') in COUNTERS:
                            counts[COUNTERS[event['kind']]] += 1
            except (OSError, ValueError):
                counts['unavailableEventStreams'] += 1
    return dict(counts), verified


class Systemd:
    def __init__(self, unit, manual_unit):
        if not re.fullmatch(r'[a-zA-Z0-9_.@-]+\.service', unit):
            raise ValueError('Invalid systemd service unit')
        if not re.fullmatch(r'[a-zA-Z0-9_.-]+@\.service', manual_unit):
            raise ValueError('Invalid manual service template')
        self.unit, self.manual_unit = unit, manual_unit

    def request_unit(self, key):
        return self.manual_unit.replace('@.service', '@' + str(uuid.UUID(key)) + '.service')

    def start(self, key):
        subprocess.run(['systemctl', '--user', 'start', '--no-block', self.request_unit(key)], check=True, timeout=10, capture_output=True)

    def request_active(self, key):
        return self.unit_active(self.request_unit(key))

    def active(self):
        return self.unit_active(self.unit)

    def unit_active(self, unit):
        result = subprocess.run(['systemctl', '--user', 'show', unit, '--property=ActiveState', '--value'], check=True, timeout=10, capture_output=True, text=True)
        return result.stdout.strip() in ('active', 'activating', 'deactivating', 'reloading')
