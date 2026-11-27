import hashlib
import io
import pathlib
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import updater


class UpdaterTests(unittest.TestCase):
    def test_versions_only_advance_base_release(self):
        for current, target, allowed in [
            ('0.9.0', 'v0.10.0', True),
            ('0.9.0-fleet.1', 'v0.10.0', True),
            ('0.9.0-fleet.1', 'v0.9.0', False),
            ('0.10.0', 'v0.9.0', False),
            ('dev', 'v0.10.0', False),
            ('0.9.0', 'v0.10.0;id', False),
        ]:
            with self.subTest(current=current, target=target):
                self.assertEqual(updater.is_newer(target, current), allowed)

    def test_download_extracts_only_checked_regular_binary(self):
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode='w:gz') as archive:
            entry = tarfile.TarInfo('touchgrass')
            entry.size = 6
            archive.addfile(entry, io.BytesIO(b'binary'))
            other = tarfile.TarInfo('../../outside')
            other.size = 4
            archive.addfile(other, io.BytesIO(b'evil'))
        payload = data.getvalue()
        name = 'touchgrass_0.10.0_linux_arm64.tar.gz'
        checksum = hashlib.sha256(payload).hexdigest() + '  ' + name + '\n'
        with tempfile.TemporaryDirectory() as directory:
            dest = pathlib.Path(directory) / 'binary'
            with patch.object(updater, 'fetch', side_effect=[payload, checksum.encode()]):
                updater.download_release('v0.10.0', 'arm64', dest)
            self.assertEqual(dest.read_bytes(), b'binary')
            self.assertEqual(list(dest.parent.iterdir()), [dest])
            with patch.object(updater, 'fetch', side_effect=[payload, ('0' * 64 + '  ' + name).encode()]):
                with self.assertRaises(ValueError):
                    updater.download_release('v0.10.0', 'arm64', dest)

    def test_rejects_symlink_binary(self):
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode='w:gz') as archive:
            entry = tarfile.TarInfo('touchgrass')
            entry.type = tarfile.SYMTYPE
            entry.linkname = '/bin/sh'
            archive.addfile(entry)
        payload = data.getvalue()
        checksum = hashlib.sha256(payload).hexdigest() + '  touchgrass_0.10.0_linux_arm64.tar.gz\n'
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(updater, 'fetch', side_effect=[payload, checksum.encode()]):
                with self.assertRaises(ValueError):
                    updater.download_release('v0.10.0', 'arm64', pathlib.Path(directory) / 'binary')

    def test_checksum_requires_exact_unique_name(self):
        for text in ['a' * 64 + '  bad-archive.tar.gz',
                     ('a' * 64 + '  archive.tar.gz\n') * 2]:
            with self.subTest(text=text):
                with self.assertRaises(ValueError):
                    updater.release_checksum(text, 'archive.tar.gz')


class InstallFlowTests(unittest.TestCase):
    def test_backup_finishes_while_wal_writes_continue(self):
        import sqlite3
        from contextlib import closing
        connect = sqlite3.connect
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source, destination = root / 'busy.db', root / 'backup.db'
            with closing(connect(source)) as writer:
                writer.execute('pragma journal_mode=wal')
                writer.execute('create table sample (generation integer, payload blob)')
                writer.execute('insert into sample values (0, zeroblob(8388608))')
                writer.commit()
                steps = []

                class BusyConnection(sqlite3.Connection):
                    def backup(self, target, **kwargs):
                        progress = kwargs.get('progress')

                        def write_during_copy(status, remaining, total):
                            steps.append(remaining)
                            if len(steps) > 8:
                                raise RuntimeError('backup starved by concurrent writes')
                            writer.execute('update sample set generation=generation+1')
                            writer.commit()
                            if progress:
                                progress(status, remaining, total)

                        super().backup(target, **{**kwargs, 'progress': write_during_copy})

                def connection(path, **kwargs):
                    return connect(path, **kwargs, factory=BusyConnection)

                with patch.object(updater.sqlite3, 'connect', side_effect=connection):
                    updater.backup_database(source, destination)
                self.assertGreater(len(steps), 1)
                with closing(connect(destination)) as backup:
                    self.assertEqual(backup.execute('select generation from sample').fetchone(), (0,))

    def exercise(self, failure=False, stop_failure=False, recovery_failure=False):
        import json
        import sqlite3
        from contextlib import ExitStack
        with tempfile.TemporaryDirectory() as directory, ExitStack() as stack:
            root = pathlib.Path(directory)
            binary = root / 'touchgrass'
            binary.write_bytes(b'old binary')
            db = root / 'app.db'
            with sqlite3.connect(db) as connection:
                connection.execute('create table sample (value text)')
                connection.execute("insert into sample values ('preserve')")
            connection.close()
            updater.write_json(root / 'status.json', {'tag': 'v0.10.0', 'state': 'pending'})
            stack.enter_context(patch.object(updater, 'STATE', root))
            stack.enter_context(patch.object(updater, 'BINARY', binary))
            stack.enter_context(patch.object(updater, 'trusted'))
            stack.enter_context(patch.object(updater, 'health', return_value='0.9.0-fleet.1'))
            commands = stack.enter_context(patch.object(updater, 'command'))
            if stop_failure:
                commands.side_effect = [RuntimeError('stop timed out'), '']
            if recovery_failure:
                commands.side_effect = ['', '', RuntimeError('recovery failed')]
            stack.enter_context(patch.object(updater, 'download_release', side_effect=lambda _t, _a, p: p.write_bytes(b'new binary')))
            wait = stack.enter_context(patch.object(updater, 'wait_healthy', side_effect=[RuntimeError('unhealthy'), None] if failure else [None]))
            config = {'arch': 'arm64', 'database': str(db), 'service': 'example.service'}
            if failure or stop_failure:
                message = 'recovery failed' if recovery_failure else 'stop timed out' if stop_failure else 'unhealthy'
                with self.assertRaisesRegex(RuntimeError, message):
                    updater.run(config, 'v0.10.0', '0.9.0-fleet.1')
            else:
                updater.run(config, 'v0.10.0', '0.9.0-fleet.1')
            self.assertEqual(binary.read_bytes(), b'old binary' if (failure and not recovery_failure) or stop_failure else b'new binary')
            self.assertEqual(json.loads((root / 'status.json').read_text())['state'], 'failed' if failure or stop_failure else 'succeeded')
            self.assertEqual(len(list(root.glob('backup-*.db'))), 1)
            self.assertEqual(next(root.glob('backup-*.binary')).read_bytes(), b'old binary')
            with sqlite3.connect(db) as connection:
                self.assertEqual(connection.execute('select value from sample').fetchone(), ('preserve',))
            connection.close()
            self.assertEqual(commands.call_count, 3 if recovery_failure else 4 if failure else 2)
            self.assertEqual(wait.call_count, 2 if failure and not recovery_failure else 1)

    def test_success_retains_database_and_backup(self):
        self.exercise()

    def test_failed_health_restores_binary_without_rewinding_database(self):
        self.exercise(failure=True)

    def test_stop_timeout_still_restarts_original_service(self):
        self.exercise(stop_failure=True)

    def test_failed_recovery_retains_original_binary_and_database_backup(self):
        self.exercise(failure=True, recovery_failure=True)

    def test_failed_backup_does_not_leave_a_restore_candidate(self):
        import sqlite3
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source, destination = root / 'broken.db', root / 'backup.db'
            source.write_bytes(b'not a sqlite database')
            with self.assertRaises(sqlite3.DatabaseError):
                updater.backup_database(source, destination)
            self.assertFalse(destination.exists())

    def test_download_failure_leaves_no_orphan_rollback_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            binary = root / 'touchgrass'
            binary.write_bytes(b'old binary')
            updater.write_json(root / 'status.json', {'tag': 'v0.10.0', 'state': 'pending'})
            with patch.object(updater, 'STATE', root), patch.object(updater, 'BINARY', binary), patch.object(updater, 'trusted'):
                with patch.object(updater, 'health', return_value='0.9.0'), patch.object(updater, 'download_release', side_effect=ValueError('checksum')):
                    with self.assertRaisesRegex(ValueError, 'checksum'):
                        updater.run({'arch': 'arm64'}, 'v0.10.0', '0.9.0')
            self.assertEqual(list(root.glob('backup-*')), [])
            self.assertEqual(binary.read_bytes(), b'old binary')

    def test_lock_refuses_concurrent_install(self):
        with tempfile.TemporaryDirectory() as directory:
            with updater.lock(pathlib.Path(directory) / 'lock'):
                with self.assertRaisesRegex(RuntimeError, 'another deployment'):
                    with updater.lock(pathlib.Path(directory) / 'lock'):
                        self.fail('second lock acquired')

class CancellationTests(unittest.TestCase):
    def test_health_poll_does_not_swallow_update_cancellation(self):
        with patch.object(updater, 'health', side_effect=updater.UpdateCancelled('deadline')) as health:
            with patch.object(updater.time, 'sleep') as sleep:
                with self.assertRaises(updater.UpdateCancelled):
                    updater.wait_healthy({}, '0.10.0')
                health.assert_called_once()
                sleep.assert_not_called()

    def test_health_poll_has_a_wall_clock_deadline(self):
        with patch.object(updater, 'health', side_effect=RuntimeError('not ready')) as health:
            with patch.object(updater.time, 'monotonic', side_effect=[0, 0, 60]), patch.object(updater.time, 'sleep'):
                with self.assertRaisesRegex(RuntimeError, 'did not become healthy'):
                    updater.wait_healthy({}, '0.10.0')
                health.assert_called_once()

if __name__ == '__main__':
    unittest.main()
