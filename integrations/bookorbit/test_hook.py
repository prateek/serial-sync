import copy
import hashlib
import tempfile
import unittest
from pathlib import Path

from hook import Publisher
from adopt import adopt


class Reader:
    def __init__(self):
        self.scans = 0
        self.ready = True

    def scan(self):
        self.scans += 1

    def catalog(self, _):
        return {}

    def verify(self, _, path, expected):
        if not self.ready:
            raise RuntimeError('Reader has stale bytes')
        return {'book_id': 1, 'file_id': 1, 'url': 'https://reader.example/read/1/1'}


class PublisherTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.reader = Reader()
        self.publisher = Publisher({'state_dir': str(self.root/'state'), 'library_root': str(self.root/'library'),
                                    'reader_root': '/books', 'public_url': 'https://reader.example',
                                    'announce': True}, self.reader)
        self.sent = []
        self.publisher.notify = lambda *payload: self.sent.append(payload) or True

    def candidate(self, number=1, contents=b'original'):
        source = self.root / f'captured-{number}-{hashlib.sha256(contents).hexdigest()}.epub'
        source.write_bytes(contents)
        return {'version': 2, 'action': 'publish', 'event_id': f'event-{number}-{contents.hex()}',
                'source': {'id': 'source'}, 'track': {'track_key': 'series', 'track_name': 'Series'},
                'release': {'id': f'release-{number}'},
                'artifact': {'id': f'artifact-{number}-{contents.hex()}', 'mime_type': 'application/epub+zip',
                             'filename': f'chapter-{number}.epub', 'sha256': hashlib.sha256(contents).hexdigest(),
                             'storage_ref': str(source)}}

    def previous(self, *candidates):
        return [{**candidate, 'record': {'status': 'published', 'filename': candidate['artifact']['filename']}}
                for candidate in candidates]

    def prepare(self, *candidates, maintenance=False, previous=None):
        event = {'version': 1, 'action': 'prepare', 'maintenance': maintenance, 'candidates': candidates}
        if previous is not None:
            event['previous'] = previous
        self.publisher.handle(event)

    def complete(self, success=True):
        self.publisher.handle({'version': 1, 'action': 'complete', 'succeeded': success})

    def test_one_scan_and_notification_for_batch_and_quiet_retry(self):
        a, b = self.candidate(), self.candidate(2)
        self.prepare(a, b)
        self.publisher.handle(a)
        self.publisher.handle(b)
        self.complete()
        self.assertEqual(self.reader.scans, 1)
        self.assertEqual(len(self.sent), 1)
        self.assertIn('2 new release', self.sent[0][1])
        self.prepare(a, b)
        self.publisher.handle(a)
        self.complete()
        self.assertEqual(self.reader.scans, 1)
        self.assertEqual(len(self.sent), 1)

    def test_failed_readiness_does_not_acknowledge_or_queue_new_chapters(self):
        a = self.candidate()
        self.reader.ready = False
        with self.assertRaisesRegex(RuntimeError, 'stale bytes'):
            self.prepare(a)
        self.assertEqual(self.publisher.state.pending(), [])
        self.assertIsNone(self.publisher.state.get('events', a['event_id']))
        self.reader.ready = True
        self.prepare(a)
        self.publisher.handle(a)
        self.complete()
        self.assertEqual(len(self.sent), 1)

    def test_notification_failure_preserves_readiness_and_outbox(self):
        a = self.candidate()
        self.prepare(a)
        self.publisher.handle(a)
        def fail(*_):
            raise RuntimeError('offline')
        self.publisher.notify = fail
        with self.assertRaisesRegex(RuntimeError, 'offline'):
            self.complete()
        self.assertTrue(self.publisher.owned(self.publisher.destination(a))['ready'])
        self.assertEqual(len(self.publisher.state.pending()), 1)
        self.publisher.notify = lambda *payload: self.sent.append(payload) or True
        self.complete()
        self.complete()
        self.assertEqual(len(self.sent), 1)

    def test_rebuild_is_quiet_and_prepare_does_not_overwrite_existing_copy(self):
        a = self.candidate()
        self.prepare(a)
        self.publisher.handle(a)
        self.complete()
        b = self.candidate(contents=b'enriched')
        self.prepare(b, maintenance=True)
        self.assertEqual(self.publisher.destination(a).read_bytes(), b'original')
        self.publisher.handle(b)
        self.complete()
        self.assertEqual(self.publisher.destination(b).read_bytes(), b'enriched')
        self.assertEqual(len(self.sent), 1)

    def test_user_modified_and_unowned_files_are_preserved(self):
        a = self.candidate()
        path = self.publisher.destination(a)
        path.parent.mkdir(parents=True)
        path.write_bytes(b'user file')
        with self.assertRaisesRegex(RuntimeError, 'ownership conflict'):
            self.prepare(a)
        self.assertEqual(path.read_bytes(), b'user file')
        path.unlink()
        self.prepare(a)
        self.publisher.handle(a)
        path.write_bytes(b'user edit')
        with self.assertRaisesRegex(RuntimeError, 'ownership conflict'):
            self.prepare(a)
        self.assertEqual(path.read_bytes(), b'user edit')

    def test_maintenance_imports_independent_replacements_in_one_scan(self):
        old = [self.candidate(number) for number in (1, 2, 3)]
        self.prepare(*old)
        for candidate in old:
            self.publisher.handle(candidate)
        self.complete()
        replacements = [self.candidate(number, b'enriched') for number in (1, 2, 3)]
        self.prepare(*replacements, maintenance=True, previous=self.previous(*old))
        self.assertEqual(self.reader.scans, 2)
        for candidate in replacements:
            self.assertEqual(self.publisher.destination(candidate).read_bytes(), b'enriched')
            self.publisher.handle(candidate)
        self.assertEqual(self.reader.scans, 2)
        self.complete()
        self.assertEqual(len(self.sent), 1)

    def test_ordinary_prepare_does_not_batch_replacements_even_with_predecessors(self):
        old = self.candidate()
        self.prepare(old)
        self.publisher.handle(old)
        replacement = self.candidate(contents=b'enriched')
        self.prepare(replacement, previous=self.previous(old))
        self.assertEqual(self.publisher.destination(old).read_bytes(), b'original')

    def test_maintenance_rejects_same_path_volume_split_before_staging(self):
        old = self.candidate(contents=b'original combined volume')
        old['release'] = {}
        old['volume'] = {'members': [{'release_id': 'release-1'}, {'release_id': 'release-2'}]}
        self.prepare(old)
        self.publisher.handle(old)
        receipt = self.publisher.owned(self.publisher.destination(old))
        replacement = self.candidate(contents=b'first part')
        other = self.candidate(2, b'second part')
        with self.assertRaisesRegex(RuntimeError, 'release coverage'):
            self.prepare(replacement, other, maintenance=True, previous=self.previous(old))
        self.assertEqual(self.publisher.destination(old).read_bytes(), b'original combined volume')
        self.assertEqual(self.publisher.owned(self.publisher.destination(old)), receipt)
        self.assertFalse(self.publisher.destination(other).exists())

    def test_same_path_replacement_rejects_any_changed_volume_membership(self):
        old = self.candidate(contents=b'original combined volume')
        old['volume'] = {'members': [{'release_id': 'release-1'}, {'release_id': 'release-2'}]}
        self.prepare(old)
        self.publisher.handle(old)
        receipt = self.publisher.owned(self.publisher.destination(old))
        for members in (['release-1'], ['release-1', 'release-2', 'release-3'], ['release-3', 'release-4']):
            replacement = self.candidate(contents=b'changed membership')
            replacement['volume'] = {'members': [{'release_id': identity} for identity in members]}
            with self.subTest(members=members):
                with self.assertRaisesRegex(RuntimeError, 'release coverage.*distinct filename'):
                    self.publisher.handle(replacement)
                self.assertEqual(self.publisher.destination(old).read_bytes(), b'original combined volume')
                self.assertEqual(self.publisher.owned(self.publisher.destination(old)), receipt)
        replacement = self.candidate(contents=b'corrected combined volume')
        replacement['volume'] = old['volume']
        self.publisher.handle(replacement)
        self.assertEqual(self.publisher.destination(old).read_bytes(), b'corrected combined volume')

    def test_legacy_coverage_requires_explicit_single_predecessor_proof(self):
        old = self.candidate()
        self.prepare(old)
        self.publisher.handle(old)
        path = self.publisher.destination(old)
        receipt = self.publisher.owned(path)
        receipt.pop('release_ids', None)
        self.publisher.state.put('files', str(path), receipt)
        replacement = self.candidate(contents=b'enriched')
        with self.assertRaisesRegex(RuntimeError, 'release coverage.*explicit rebuild'):
            self.publisher.handle(replacement)
        self.assertEqual(path.read_bytes(), b'original')
        self.assertEqual(self.publisher.owned(path), receipt)
        self.prepare(replacement, maintenance=True, previous=self.previous(old))
        self.publisher.handle(replacement)
        self.assertEqual(path.read_bytes(), b'enriched')
        self.assertEqual(self.publisher.owned(path)['release_ids'], ['release-1'])

    def test_maintenance_upgrades_unchanged_legacy_receipt_for_future_corrections(self):
        old = self.candidate()
        self.prepare(old)
        self.publisher.handle(old)
        path = self.publisher.destination(old)
        receipt = self.publisher.owned(path)
        receipt.pop('release_ids')
        self.publisher.state.put('files', str(path), receipt)
        self.prepare(old, maintenance=True, previous=self.previous(old))
        self.assertEqual(self.reader.scans, 1)
        self.assertEqual(self.publisher.owned(path)['release_ids'], ['release-1'])
        corrected = self.candidate(contents=b'corrected')
        self.publisher.handle(corrected)
        self.assertEqual(path.read_bytes(), b'corrected')

    def test_maintenance_requires_same_release_path_and_owned_predecessor(self):
        old = self.candidate()
        self.prepare(old)
        self.publisher.handle(old)
        replacement = self.candidate(contents=b'enriched')
        variants = [[], self.previous(old), self.previous(old), self.previous(old), self.previous(old)]
        variants[1][0]['release'] = {'id': 'different-release'}
        variants[2][0]['record']['filename'] = 'different-path.epub'
        variants[3][0]['artifact'] = {**old['artifact'], 'id': 'different-artifact'}
        variants[4][0]['artifact'] = {**old['artifact'], 'sha256': 'different-hash'}
        for previous in variants:
            with self.subTest(previous=previous):
                self.prepare(replacement, maintenance=True, previous=previous)
                self.assertEqual(self.publisher.destination(old).read_bytes(), b'original')
        volume = copy.deepcopy(replacement)
        volume['volume'] = {'members': [{'release_id': 'release-1'}]}
        self.prepare(volume, maintenance=True, previous=self.previous(old))
        self.assertEqual(self.publisher.destination(old).read_bytes(), b'original')

    def test_maintenance_rejects_malformed_predecessors_before_writing(self):
        old = self.candidate()
        self.prepare(old)
        self.publisher.handle(old)
        replacement = self.candidate(contents=b'enriched')
        for previous in ({}, [None], [{}]):
            with self.subTest(previous=previous):
                with self.assertRaisesRegex(RuntimeError, 'previous publication'):
                    self.prepare(replacement, maintenance=True, previous=previous)
                self.assertEqual(self.publisher.destination(old).read_bytes(), b'original')

    def test_maintenance_preflights_every_replacement_before_staging(self):
        old = [self.candidate(number) for number in (1, 2)]
        self.prepare(*old)
        for candidate in old:
            self.publisher.handle(candidate)
        replacements = [self.candidate(number, b'enriched') for number in (1, 2)]
        Path(replacements[1]['artifact']['storage_ref']).write_bytes(b'corrupted')
        with self.assertRaisesRegex(RuntimeError, 'Saved artifact has changed'):
            self.prepare(*replacements, maintenance=True, previous=self.previous(*old))
        for candidate in old:
            self.assertEqual(self.publisher.destination(candidate).read_bytes(), b'original')

    def test_maintenance_restart_recovers_partial_staging(self):
        old = [self.candidate(number) for number in (1, 2)]
        self.prepare(*old)
        for candidate in old:
            self.publisher.handle(candidate)
        replacements = [self.candidate(number, b'enriched') for number in (1, 2)]
        put = self.publisher.state.put

        def interrupted_put(category, identity, value):
            put(category, identity, value)
            if category == 'writes' and identity == str(self.publisher.destination(replacements[1])):
                raise RuntimeError('Process stopped before second replacement')

        self.publisher.state.put = interrupted_put
        with self.assertRaisesRegex(RuntimeError, 'Process stopped'):
            self.prepare(*replacements, maintenance=True, previous=self.previous(*old))
        self.assertEqual(self.publisher.destination(old[0]).read_bytes(), b'enriched')
        self.assertEqual(self.publisher.destination(old[1]).read_bytes(), b'original')
        restarted = Publisher(self.publisher.config, self.reader)
        restarted.prepare({'candidates': replacements, 'maintenance': True, 'previous': self.previous(*old)})
        for candidate in replacements:
            restarted.handle(candidate)
            self.assertEqual(restarted.destination(candidate).read_bytes(), b'enriched')
        self.assertEqual(self.reader.scans, 2)

    def test_maintenance_restart_recovers_partial_readiness_without_announcing(self):
        old = [self.candidate(number) for number in (1, 2)]
        self.prepare(*old)
        for candidate in old:
            self.publisher.handle(candidate)
        self.complete()
        replacements = [self.candidate(number, b'enriched') for number in (1, 2)]
        verify = self.reader.verify

        def interrupted_verify(catalog, path, expected):
            if path.endswith('chapter-2.epub'):
                raise RuntimeError('Process stopped during readiness verification')
            return verify(catalog, path, expected)

        self.reader.verify = interrupted_verify
        with self.assertRaisesRegex(RuntimeError, 'Process stopped'):
            self.prepare(*replacements, maintenance=True, previous=self.previous(*old))
        for candidate in replacements:
            self.assertIsNone(self.publisher.state.get('events', candidate['event_id']))
        self.reader.verify = verify
        restarted = Publisher(self.publisher.config, self.reader)
        restarted.notify = self.publisher.notify
        restarted.prepare({'candidates': replacements, 'maintenance': True, 'previous': self.previous(*old)})
        for candidate in replacements:
            restarted.handle(candidate)
            self.assertTrue(restarted.owned(restarted.destination(candidate))['ready'])
        restarted.complete({'succeeded': True})
        self.assertEqual(len(self.sent), 1)

    def test_retirement_requires_readiness_and_preserves_active_progress(self):
        old, replacement = self.candidate(), self.candidate(2)
        self.prepare(old)
        self.publisher.handle(old)
        event = {'version': 2, 'action': 'supersede', 'event_id': 'retirement',
                 'previous': old, 'replacements': [replacement]}
        with self.assertRaisesRegex(RuntimeError, 'every replacement is readable'):
            self.publisher.handle(event)
        self.prepare(replacement)
        self.publisher.handle(replacement)
        path = self.publisher.destination(old)
        def request(method, route):
            if route == 'books/1':
                return {'files': [{'id': 1, 'absolutePath': self.publisher.reader_path(path)}]}
            if route.endswith('/progress'):
                return {'percentage': 30}
            self.fail('Retired an active reading position')
        self.reader.request = request
        with self.assertRaisesRegex(RuntimeError, 'active reading position'):
            self.publisher.handle(event)
        self.assertEqual(path.read_bytes(), b'original')
        def finish(method, route):
            if route.endswith('/progress'):
                return {'percentage': 100}
            if method == 'DELETE':
                path.unlink()
                return None
            return request(method, route)
        self.reader.request = finish
        self.publisher.handle(event)
        self.publisher.handle(event)
        self.assertFalse(path.exists())

    def test_restart_recovers_a_staged_file_without_duplicate_delivery(self):
        a = self.candidate()
        self.publisher.stage(a, allow_replace=False)
        restarted = Publisher(self.publisher.config, self.reader)
        restarted.prepare({'maintenance': False, 'candidates': [a]})
        restarted.publish(a)
        self.assertTrue(restarted.owned(restarted.destination(a))['ready'])
        self.assertEqual(len(restarted.state.pending()), 1)
        restarted.publish(a)
        self.assertEqual(len(restarted.state.pending()), 1)

    def test_restart_after_notification_cleanup_does_not_renotify_remaining_releases(self):
        a, b = self.candidate(), self.candidate(2)
        self.prepare(a, b)
        self.publisher.handle(a)
        self.publisher.handle(b)
        remove = self.publisher.state.remove

        def interrupted_remove(category, identity):
            remove(category, identity)
            if category == 'outbox':
                raise RuntimeError('Process stopped during notification cleanup')

        self.publisher.state.remove = interrupted_remove
        with self.assertRaisesRegex(RuntimeError, 'Process stopped'):
            self.complete()
        self.assertEqual(len(self.sent), 1)
        restarted = Publisher(self.publisher.config, self.reader)
        restarted.notify = self.publisher.notify
        restarted.handle({'version': 1, 'action': 'complete', 'succeeded': True})
        self.assertEqual(len(self.sent), 1)
        self.assertEqual(restarted.state.pending(), [])
        for candidate in (a, b):
            self.assertIsNotNone(restarted.state.get('seen', candidate['release']['id']))

    def test_restart_after_retirement_cleanup_acknowledges_deleted_predecessor(self):
        old, replacement = self.candidate(), self.candidate(2)
        self.prepare(old, replacement)
        self.publisher.handle(old)
        self.publisher.handle(replacement)
        path = self.publisher.destination(old)

        def request(method, route):
            if method == 'DELETE':
                path.unlink()
                return None
            if route.endswith('/progress'):
                return {'percentage': 100}
            return {'files': [{'id': 1, 'absolutePath': self.publisher.reader_path(path)}]}

        self.reader.request = request
        remove = self.publisher.state.remove

        def interrupted_remove(category, identity):
            remove(category, identity)
            if category == 'files':
                raise RuntimeError('Process stopped during retirement cleanup')

        self.publisher.state.remove = interrupted_remove
        event = {'version': 2, 'action': 'supersede', 'event_id': 'retirement',
                 'previous': old, 'replacements': [replacement]}
        with self.assertRaisesRegex(RuntimeError, 'Process stopped'):
            self.publisher.handle(event)
        self.assertFalse(path.exists())
        restarted = Publisher(self.publisher.config, self.reader)
        restarted.handle(event)
        self.assertIsNotNone(restarted.state.get('events', event['event_id']))
        self.assertEqual(self.publisher.destination(replacement).read_bytes(), b'original')

    def test_path_escape_and_symlink_are_rejected(self):
        a = self.candidate()
        a['artifact']['filename'] = '../escape.epub'
        with self.assertRaisesRegex(RuntimeError, 'path component'):
            self.prepare(a)
        a = self.candidate()
        path = self.publisher.destination(a)
        path.parent.mkdir(parents=True)
        path.symlink_to(a['artifact']['storage_ref'])
        with self.assertRaisesRegex(RuntimeError, 'symlink'):
            self.prepare(a)

    def test_adoption_checks_all_predecessors_before_recording_ownership(self):
        a, b = self.candidate(), self.candidate(2)
        manifest = {}
        for candidate in (a, b):
            path = self.publisher.destination(candidate)
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b'original')
            manifest[str(path.relative_to((self.root/'library').resolve()))] = {
                'sha256': candidate['artifact']['sha256'],
                'artifact_id': candidate['artifact']['id'],
            }
        self.publisher.destination(b).write_bytes(b'user edit')
        with self.assertRaisesRegex(RuntimeError, 'differs from the migration manifest'):
            adopt(self.publisher, manifest)
        self.assertFalse(self.publisher.owned(self.publisher.destination(a)))
        self.publisher.destination(b).write_bytes(b'original')
        self.assertEqual(adopt(self.publisher, manifest), 2)
        self.assertFalse(self.publisher.owned(self.publisher.destination(a))['ready'])
        self.assertEqual(self.publisher.state.pending(), [])
        self.prepare(a, b)
        self.publisher.handle(a)
        self.assertTrue(self.publisher.owned(self.publisher.destination(a))['ready'])
        self.assertEqual(adopt(self.publisher, manifest), 0)


if __name__ == '__main__':
    unittest.main()
