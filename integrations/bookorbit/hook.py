#!/usr/bin/env python3
import fcntl
import hashlib
import json
import os
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

from api import APIError, BookOrbit
from state import State, file_hash, key


class Publisher:
    def __init__(self, config, api=None):
        self.config = config
        muted = config.get('muted_series', [])
        if not isinstance(muted, list) or any(not isinstance(item, str) or not item.strip() for item in muted):
            raise RuntimeError('muted_series must be a list of nonempty series IDs')
        self.muted_series = set(muted)
        self.state = State(config['state_dir'])
        self.api = api or BookOrbit(config)
        self.root = Path(config['library_root']).resolve()

    def destination(self, candidate):
        if candidate['artifact']['mime_type'] != 'application/epub+zip':
            raise RuntimeError('The BookOrbit adapter requires EPUB publication')
        parts = [candidate['source']['id'], candidate['track']['track_key'], candidate['artifact']['filename']]
        if any(not part or part in ('.', '..') or Path(part).name != part or '\\' in part for part in parts):
            raise RuntimeError('Invalid publication path component')
        path = self.root.joinpath(*parts)
        for parent in [path, *path.parents]:
            if parent == self.root:
                break
            if parent.is_symlink():
                raise RuntimeError(f'Refusing a symlink in publication path: {path}')
        path.resolve().relative_to(self.root)
        return path

    def reader_path(self, path):
        return self.config['reader_root'].rstrip('/') + '/' + path.relative_to(self.root).as_posix()

    def owned(self, path):
        return self.state.get('files', str(path), {})

    def release_ids(self, candidate):
        volume = candidate.get('volume')
        identities = ([member.get('release_id') for member in volume.get('members', [])]
                      if volume is not None else [candidate.get('release', {}).get('id')])
        if not identities or any(not isinstance(identity, str) or not identity for identity in identities):
            raise RuntimeError('Publication has invalid release coverage')
        return sorted(set(identities))

    def check_replacement_coverage(self, candidate, owned):
        previous = owned.get('release_ids')
        if not isinstance(previous, list) or not previous:
            raise RuntimeError('Cannot prove existing release coverage; retry a single through an explicit rebuild, '
                               'or use a distinct filename and an ordered migration for a volume')
        if sorted(set(previous)) != self.release_ids(candidate):
            raise RuntimeError('Cannot replace a path with different release coverage; use a distinct filename '
                               'and an ordered migration that verifies replacements before retiring the old copy')

    def check_ownership(self, candidate):
        path = self.destination(candidate)
        expected = candidate['artifact']['sha256']
        current = file_hash(path)
        owned = self.owned(path)
        intent = self.state.get('writes', str(path), {})
        recognized = current is None or current == owned.get('sha256') or current == intent.get('sha256')
        if not recognized and not (self.config.get('adopt_existing', False) and current == expected):
            raise RuntimeError(f'Publication ownership conflict: {path}')
        return path, current

    def stage(self, candidate, allow_replace):
        path, current = self.check_ownership(candidate)
        artifact = candidate['artifact']
        expected = artifact['sha256']
        owned = self.owned(path)
        if current == expected:
            return path, not (owned.get('sha256') == expected and owned.get('ready'))
        if current is not None and not allow_replace:
            return None, False
        if current is not None:
            self.check_replacement_coverage(candidate, owned)
        data = Path(artifact['storage_ref']).read_bytes()
        if hashlib.sha256(data).hexdigest() != expected:
            raise RuntimeError(f'Saved artifact has changed: {artifact["id"]}')
        path.parent.mkdir(parents=True, exist_ok=True)
        self.state.put('writes', str(path), {'sha256': expected, 'previous': current})
        fd, temporary = tempfile.mkstemp(prefix='.serial-sync-', suffix='.tmp', dir=path.parent)
        try:
            with os.fdopen(fd, 'wb') as output:
                output.write(data)
                output.flush()
                os.fchmod(output.fileno(), 0o644)
                os.fsync(output.fileno())
            if current is None:
                os.link(temporary, path)
            else:
                if file_hash(path) != current:
                    raise RuntimeError(f'Publication changed during replacement: {path}')
                os.replace(temporary, path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
        return path, True

    def reconcile(self, candidates):
        if not candidates:
            return
        self.api.scan()
        cached = self.state.get('catalog', 'books', {})
        catalog = self.api.catalog(cached)
        self.state.put('catalog', 'books', catalog)
        for candidate in candidates:
            path = self.destination(candidate)
            expected = candidate['artifact']['sha256']
            if file_hash(path) != expected:
                raise RuntimeError(f'Publication changed before import verification: {path}')
            receipt = self.api.verify(catalog, self.reader_path(path), expected)
            self.state.put('files', str(path), {**receipt, 'sha256': expected, 'ready': True,
                                              'artifact_id': candidate['artifact']['id'],
                                              'release_ids': self.release_ids(candidate)})

    def prepare(self, event):
        self.state.put('context', 'delivery', {'maintenance': event.get('maintenance', False)})
        candidates = event.get('candidates', [])
        replacements = set()
        if event.get('maintenance', False) and 'previous' in event:
            replacements = self.maintenance_replacements(candidates, event['previous'])
        pending = []
        for candidate in candidates:
            path, needed = self.stage(candidate, allow_replace=candidate['artifact']['id'] in replacements)
            if path is not None and needed:
                pending.append(candidate)
        self.reconcile(pending)

    def maintenance_replacements(self, candidates, previous):
        if not isinstance(previous, list):
            raise RuntimeError('Invalid previous publications: expected a list')
        by_path = {}
        for prior in previous:
            try:
                if prior['record']['status'] != 'published' or not prior['artifact']['id'] or not prior['artifact']['sha256']:
                    raise ValueError('Invalid publication receipt')
                prior = {**prior, 'artifact': {**prior['artifact'], 'filename': prior['record']['filename']}}
                path = self.destination(prior)
            except (KeyError, TypeError, ValueError) as error:
                raise RuntimeError('Invalid previous publication identity') from error
            by_path.setdefault(path, []).append(prior)
        replacements, destinations, identities, upgrades = set(), set(), set(), {}
        for candidate in candidates:
            path, current = self.check_ownership(candidate)
            artifact = candidate['artifact']
            if path in destinations or not artifact['id'] or artifact['id'] in identities:
                raise RuntimeError('Ambiguous maintenance publication identity')
            destinations.add(path)
            identities.add(artifact['id'])
            if current != artifact['sha256'] and file_hash(artifact['storage_ref']) != artifact['sha256']:
                raise RuntimeError(f'Saved artifact has changed: {artifact["id"]}')
            release_id = candidate.get('release', {}).get('id')
            predecessors = by_path.get(path, [])
            owned = self.owned(path)
            proven_single = (not candidate.get('volume') and release_id and predecessors and
                             all(prior.get('release', {}).get('id') == release_id for prior in predecessors) and
                             any(prior['artifact']['id'] == owned.get('artifact_id') and
                                 prior['artifact']['sha256'] == owned.get('sha256') for prior in predecessors))
            if proven_single:
                replacements.add(artifact['id'])
                if 'release_ids' not in owned:
                    owned = {**owned, 'release_ids': self.release_ids(candidate)}
                    upgrades[path] = owned
            if current is not None and current != artifact['sha256']:
                self.check_replacement_coverage(candidate, owned)
        for path, owned in upgrades.items():
            self.state.put('files', str(path), owned)
        return replacements

    def publish(self, event):
        candidate = event
        path, needed = self.stage(candidate, allow_replace=True)
        if needed:
            self.reconcile([candidate])
        receipt = self.owned(path)
        if not receipt.get('ready') or receipt['sha256'] != candidate['artifact']['sha256']:
            raise RuntimeError('No readable-version receipt for publication')
        coverage = self.release_ids(candidate)
        maintenance = self.state.get('context', 'delivery', {}).get('maintenance', True)
        for release_id in coverage:
            if self.state.get('seen', release_id) is not None:
                continue
            if self.config.get('announce', False) and not maintenance:
                self.state.put('outbox', release_id, {'id': release_id, 'series': candidate['track']['track_name'],
                                                    'series_id': candidate['track']['track_key'],
                                                    'url': receipt['url']})
            else:
                self.state.put('seen', release_id, {'maintenance': maintenance})
        self.state.put('events', event['event_id'], {'artifact_id': candidate['artifact']['id'], **receipt})
        return receipt

    def supersede(self, event):
        previous = event['previous']
        old_path = self.destination(previous)
        old_hash = previous['artifact']['sha256']
        same_path = False
        for replacement in event.get('replacements', []):
            path = self.destination(replacement)
            receipt = self.owned(path)
            if not receipt.get('ready') or receipt.get('sha256') != replacement['artifact']['sha256'] or file_hash(path) != receipt['sha256']:
                raise RuntimeError('Cannot retire a file before every replacement is readable')
            same_path = same_path or path == old_path
        if same_path:
            return
        current = file_hash(old_path)
        receipt = self.owned(old_path)
        if receipt.get('sha256') != old_hash or current not in (None, old_hash):
            raise RuntimeError(f'Refusing to retire a modified or unrelated file: {old_path}')
        try:
            book = self.api.request('GET', f'books/{receipt["book_id"]}')
            matches = [file for file in book['files'] if file['id'] == receipt['file_id'] and file['absolutePath'] == self.reader_path(old_path)]
            if len(matches) != 1:
                raise RuntimeError('Retirement identity no longer matches BookOrbit')
            progress = self.api.request('GET', f'books/files/{receipt["file_id"]}/progress')
            if progress and 0 < progress.get('percentage', 0) < 100:
                raise RuntimeError('Retirement would remove an active reading position; retain stable copies or migrate progress first')
            self.api.request('DELETE', f'books/files/{receipt["file_id"]}')
        except APIError as error:
            if error.status != 404 or current is not None:
                raise
        if old_path.exists():
            raise RuntimeError('BookOrbit did not retire the acknowledged file')
        self.state.put('events', event['event_id'], {'retired': str(old_path)})
        self.state.remove('files', str(old_path))

    def notify(self, title, body, url):
        config = self.config.get('notification')
        if not config:
            return False
        payload = {'title': title, 'body': body, 'url': url}
        if config['kind'] == 'ntfy':
            payload = {'topic': config['topic'], 'title': title, 'message': body, 'click': url}
        request = urllib.request.Request(config['url'], json.dumps(payload).encode(), method='POST',
                                        headers={'Content-Type': 'application/json'})
        with urllib.request.urlopen(request, timeout=20) as response:
            result = json.loads(response.read())
        if config['kind'] == 'exe' and (not result.get('success') or result.get('sent', 0) < 1):
            raise RuntimeError('exe.dev did not report a notified device')
        return True

    def complete(self, event):
        while True:
            batch = self.state.get('context', 'notification_batch')
            if batch is None:
                pending = self.state.pending()
                if not pending:
                    break
                batch = {'id': key('\n'.join(sorted(item['id'] for item in pending))), 'items': pending}
                self.state.put('context', 'notification_batch', batch)
            batch_id, pending = batch['id'], batch['items']
            receipt = self.state.get('notifications', batch_id)
            if receipt is None:
                if self.muted_series and any(not item.get('series_id') for item in pending):
                    raise RuntimeError('Queued notifications lack series IDs; drain the old outbox before enabling muted_series')
                announced = [item for item in pending if item.get('series_id') not in self.muted_series]
                receipt = {'notified_ids': [item['id'] for item in announced]}
                if announced:
                    grouped = {}
                    for item in announced:
                        grouped[item['series']] = grouped.get(item['series'], 0) + 1
                    body = '\n'.join(f'{series}: {count} new release(s)' for series, count in sorted(grouped.items()))
                    link = announced[0]['url'] if len(grouped) == 1 else self.config['public_url']
                    if not self.notify('New chapters ready', body, link):
                        raise RuntimeError('New chapters are ready, but no notification provider is configured')
                    receipt['delivered_at'] = time.time()
                self.state.put('notifications', batch_id, receipt)
            notified = set(receipt.get('notified_ids', [item['id'] for item in pending]))
            for item in pending:
                outcome = {'notified': batch_id} if item['id'] in notified else {'muted': True}
                self.state.put('seen', item['id'], outcome)
                self.state.remove('outbox', item['id'])
            self.state.remove('context', 'notification_batch')
        if event.get('succeeded', False):
            self.state.remove('alerts', 'failure')
        elif self.state.get('alerts', 'failure') is None:
            if self.notify('Serial-sync needs attention', 'Chapter delivery failed. Check the latest serial-sync run; existing reading copies remain available.', self.config['public_url']):
                self.state.put('alerts', 'failure', {'delivered_at': time.time()})

    def handle(self, event):
        expected_version = 1 if event['action'] in ('prepare', 'complete') else 2
        if event.get('version') != expected_version:
            raise RuntimeError('Unsupported hook protocol')
        action = event['action']
        if action not in ('prepare', 'complete', 'publish', 'supersede'):
            raise RuntimeError('Unsupported hook action')
        if action == 'supersede' and self.state.get('events', event['event_id']) is not None:
            path = self.destination(event['previous'])
            if self.owned(path).get('sha256') == event['previous']['artifact']['sha256'] and file_hash(path) is None:
                self.state.remove('files', str(path))
            return
        return getattr(self, action)(event)


def main():
    with open(sys.argv[1]) as file:
        publisher = Publisher(json.load(file))
    with (publisher.state.root / 'lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        result = publisher.handle(json.load(sys.stdin))
        if result:
            print(json.dumps(result))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(f'BookOrbit delivery: {error}', file=sys.stderr)
        sys.exit(1)
