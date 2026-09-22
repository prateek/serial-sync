#!/usr/bin/env python3
import fcntl
import json
import sys

from hook import Publisher
from state import file_hash


def adopt(publisher, manifest):
    verified = []
    for relative, record in manifest.items():
        parts = relative.split('/')
        if len(parts) != 3:
            raise RuntimeError('Adoption paths must be source/series/filename')
        candidate = {'source': {'id': parts[0]}, 'track': {'track_key': parts[1]},
                     'artifact': {'filename': parts[2], 'mime_type': 'application/epub+zip'}}
        path = publisher.destination(candidate)
        current = file_hash(path)
        owned = publisher.owned(path)
        if owned and current == owned.get('sha256'):
            continue
        if current != record['sha256']:
            raise RuntimeError(f'File differs from the migration manifest: {relative}')
        verified.append((path, record))
    for path, record in verified:
        publisher.state.put('files', str(path), {'sha256': record['sha256'], 'artifact_id': record['artifact_id'], 'ready': False})
    return len(verified)


if __name__ == '__main__':
    with open(sys.argv[1]) as file:
        publisher = Publisher(json.load(file))
    with open(sys.argv[2]) as file:
        manifest = json.load(file)
    with (publisher.state.root / 'lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        print(f'Adopted {adopt(publisher, manifest)} verified predecessor files; readiness still requires import verification.')
