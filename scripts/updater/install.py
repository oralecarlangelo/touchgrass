#!/usr/bin/python3
"""One-time native updater bootstrap. Run as root from a verified release kit."""
import argparse
import contextlib
import datetime
import importlib.util
import json
import os
import pathlib
import platform
import re
import shutil
import subprocess
import sys
import tempfile

# Explicit sibling loading also works with Python isolated mode (-I).
_spec = importlib.util.spec_from_file_location('updater', pathlib.Path(__file__).with_name('updater.py'))
updater = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(updater)


def unit_arg(value):
    if any(c in value for c in '\n\r\x00'):
        raise ValueError('unsupported systemd argument')
    return '"' + value.replace('\\', '\\\\').replace('"', '\\"').replace('%', '%%').replace('$', '$$') + '"'


def discover(properties, environment, argv):
    user = properties.get('User', '')
    if not re.fullmatch(r'[a-z_][a-z0-9_-]*', user) or user == 'root':
        raise ValueError('updater requires a dedicated non-root systemd service')
    if properties.get('NoNewPrivileges') == 'yes':
        raise ValueError('service prevents privileged updater dispatch; review the unit policy')
    if not argv or argv[1:] != ['serve']:
        raise ValueError('unsupported service command; expected the native serve command')
    address = environment.get('TOUCHGRASS_ADDR', ':8080')
    match = re.fullmatch(r'(?:127\.0\.0\.1|0\.0\.0\.0|localhost)?:([0-9]+)', address)
    if not match or not 0 < int(match[1]) < 65536:
        raise ValueError('cannot discover local console port')
    working = pathlib.Path(properties.get('WorkingDirectory') or '/')
    database = pathlib.Path(environment.get('TOUCHGRASS_DB', 'touchgrass.db'))
    if not database.is_absolute():
        database = working / database
    return {'user': user, 'database': str(database), 'args': argv[1:],
            'health_url': f'http://127.0.0.1:{match[1]}/api/health'}


def protected_directory(path):
    if not path.exists():
        updater.trusted(path.parent)
        path.mkdir(mode=0o755)
        path.chmod(0o755)
    updater.trusted(path)


def install_file(source, destination, mode):
    fd, temporary = tempfile.mkstemp(dir=destination.parent)
    os.close(fd)
    try:
        shutil.copyfile(source, temporary)
        os.chmod(temporary, mode)
        os.replace(temporary, destination)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--service', default='touchgrass.service')
    parser.add_argument('--cutover-lock', help='existing deployment lock to share')
    args = parser.parse_args()
    if os.geteuid() != 0 or platform.system() != 'Linux':
        raise ValueError('run as root on the native Linux systemd host')
    if not re.fullmatch(r'[A-Za-z0-9_-]+\.service', args.service):
        raise ValueError('invalid service name')
    os.umask(0o077)
    for executable in ('systemctl', 'systemd-run', 'sudo', 'visudo'):
        if not shutil.which(executable, path=updater.ENV['PATH']):
            raise ValueError('missing required host command: ' + executable)
    properties = dict(line.split('=', 1) for line in updater.command(
        ['systemctl', 'show', args.service, '-p', 'MainPID', '-p', 'User', '-p', 'WorkingDirectory', '-p', 'NoNewPrivileges']).splitlines())
    pid = int(properties['MainPID'])
    if pid <= 0:
        raise ValueError('native console must be running for automatic discovery')
    process = pathlib.Path('/proc') / str(pid)
    environment = dict(item.decode().split('=', 1) for item in (process / 'environ').read_bytes().split(b'\0') if b'=' in item)
    argv = [item.decode() for item in (process / 'cmdline').read_bytes().split(b'\0') if item]
    config = discover(properties, environment, argv)
    config['service'] = args.service
    config['arch'] = {'aarch64': 'arm64', 'x86_64': 'amd64'}[platform.machine()]
    current = updater.health(config)
    if args.cutover_lock:
        lock_path = pathlib.Path(args.cutover_lock)
        if not lock_path.is_absolute() or lock_path.is_symlink():
            raise ValueError('cutover lock must be an absolute non-symlink path')
        config['cutover_lock'] = str(lock_path)
    elif (updater.ROOT / 'config.json').exists():
        updater.trusted(updater.ROOT / 'config.json')
        previous = json.loads((updater.ROOT / 'config.json').read_text())
        if previous.get('cutover_lock'):
            config['cutover_lock'] = previous['cutover_lock']
    for path in (pathlib.Path('/usr/local/libexec'), updater.ROOT, updater.STATE, updater.BINARY.parent):
        protected_directory(path)
    # Serialize bootstrap with both console update requests and detached jobs.
    with contextlib.ExitStack() as stack:
        stack.enter_context(updater.lock(updater.STATE / 'request.lock'))
        stack.enter_context(updater.lock(updater.STATE / 'install.lock'))
        if config.get('cutover_lock'):
            stack.enter_context(updater.lock(config['cutover_lock'], timeout=120))
        dropin_dir = pathlib.Path('/etc/systemd/system') / (args.service + '.d')
        protected_directory(dropin_dir)
        dropin = dropin_dir / '50-touchgrass-updater.conf'
        backup = updater.STATE / ('bootstrap-' + datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S'))
        backup.mkdir(mode=0o700)
        if dropin.exists():
            updater.trusted(dropin)
            shutil.copyfile(dropin, backup / 'service.conf')
        # Copy the running executable, not an arbitrary replacement in its old
        # writable directory. It will run only as the existing service user.
        install_file(process / 'exe', backup / 'touchgrass', 0o755)
        updater.backup_database(config['database'], backup / 'database.db')
        previous_binary = updater.BINARY.exists()
        if previous_binary:
            updater.trusted(updater.BINARY)
        install_file(backup / 'touchgrass', updater.BINARY, 0o755)
        install_file(pathlib.Path(__file__).with_name('updater.py'), updater.ROOT / 'updater.py', 0o755)
        request = updater.ROOT / 'request'
        request.write_text('#!/bin/sh\nexec /usr/bin/python3 -I /usr/local/libexec/touchgrass-updater/updater.py request "$@"\n')
        request.chmod(0o755)
        updater.write_json(updater.ROOT / 'config.json', config, mode=0o600)
        sudoers = pathlib.Path('/etc/sudoers.d/touchgrass-updater')
        with tempfile.NamedTemporaryFile(mode='w', dir='/etc/sudoers.d', delete=False) as target:
            target.write(f'{config["user"]} ALL=(root) NOPASSWD: {request} *\n')
            temporary = pathlib.Path(target.name)
        try:
            temporary.chmod(0o440)
            updater.command(['visudo', '-c', '-f', str(temporary)])
            os.replace(temporary, sudoers)
        finally:
            temporary.unlink(missing_ok=True)
        dropin.write_text('[Service]\nExecStart=\nExecStart=' + unit_arg(str(updater.BINARY)) + ' serve\n'
                          'Environment=TOUCHGRASS_UPDATE_HELPER=' + str(request) + '\n'
                          'Environment=TOUCHGRASS_UPDATE_STATUS_FILE=' + str(updater.STATE / 'status.json') + '\n')
        dropin.chmod(0o644)
        try:
            updater.command(['systemctl', 'daemon-reload'])
            updater.command(['systemctl', 'restart', args.service], timeout=90)
            updater.wait_healthy(config, current)
        except BaseException:
            if (backup / 'service.conf').exists():
                install_file(backup / 'service.conf', dropin, 0o644)
            else:
                dropin.unlink(missing_ok=True)
            updater.command(['systemctl', 'daemon-reload'])
            updater.command(['systemctl', 'restart', args.service], timeout=90)
            raise
    print('Updater installed. Native service paths were discovered automatically.')
    print('Console version preserved: ' + current)
    print('Rollback files: ' + str(backup))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('Updater setup failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
