import unittest

import install


class DiscoveryTests(unittest.TestCase):
    def test_existing_paths_and_port_need_no_manual_configuration(self):
        result = install.discover(
            {'User': 'example', 'WorkingDirectory': '/srv/example', 'NoNewPrivileges': 'no'},
            {'TOUCHGRASS_DB': 'state.db', 'TOUCHGRASS_ADDR': '127.0.0.1:8199'},
            ['/srv/example/console', 'serve'],
        )
        self.assertEqual(result['database'], '/srv/example/state.db')
        self.assertEqual(result['health_url'], 'http://127.0.0.1:8199/api/health')
        self.assertEqual(result['args'], ['serve'])

    def test_privileged_or_container_install_is_not_silently_reconfigured(self):
        for user, privileges in [('', 'no'), ('root', 'no'), ('app', 'yes')]:
            with self.subTest(user=user, privileges=privileges):
                with self.assertRaises(ValueError):
                    install.discover({'User': user, 'NoNewPrivileges': privileges}, {}, ['/bin/console'])

    def test_unit_argument_escaping_does_not_expand_environment_or_specifiers(self):
        self.assertEqual(install.unit_arg('path %n $HOME "quote"'), '"path %%n $$HOME \\"quote\\""')
        with self.assertRaises(ValueError):
            install.unit_arg('bad\nline')

    def test_created_directories_are_traversable_under_private_umask(self):
        import os
        import pathlib
        import tempfile
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory, patch.object(install.updater, 'trusted'):
            destination = pathlib.Path(directory) / 'kit'
            previous = os.umask(0o077)
            try:
                install.protected_directory(destination)
            finally:
                os.umask(previous)
            self.assertEqual(destination.stat().st_mode & 0o777, 0o755)
