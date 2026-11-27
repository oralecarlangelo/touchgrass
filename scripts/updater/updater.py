#!/usr/bin/python3
"""Root-owned, fixed-upstream Touch Grass updater. No caller-supplied paths."""
import contextlib
import datetime
import fcntl
import hashlib
import io
import json
import os
import pathlib
import re
import shutil
import signal
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request

ROOT = pathlib.Path('/usr/local/libexec/touchgrass-updater')
STATE = pathlib.Path('/var/lib/touchgrass-updater')
BINARY = pathlib.Path('/usr/local/lib/touchgrass/touchgrass')
TAG = re.compile(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z')
VERSION = re.compile(r'v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?\Z')
ENV = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LANG': 'C.UTF-8'}


class UpdateCancelled(BaseException):
    """Cancellation must bypass retry loops and enter binary recovery."""


def cancel_update(_signum, _frame):
    raise UpdateCancelled('update timed out')


def is_newer(tag, current):
    left, right = TAG.fullmatch(tag), VERSION.fullmatch(current)
    return bool(left and right and tuple(map(int, left.groups())) > tuple(map(int, right.groups())))


def trusted(path):
    """Privileged code/config must have no writable or symlink ancestors."""
    path = pathlib.Path(path)
    for item in [path, *path.parents]:
        stat = item.lstat()
        if item.is_symlink() or stat.st_uid != 0 or stat.st_mode & 0o022:
            raise ValueError('updater path is not root-owned and protected')


def command(args, timeout=30):
    return subprocess.run(args, check=True, capture_output=True, text=True,
                          timeout=timeout, env=ENV).stdout.strip()


def fetch(url, limit, timeout=30):
    request = urllib.request.Request(url, headers={'User-Agent': 'touchgrass-updater'})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        data = response.read(limit + 1)
    if len(data) > limit:
        raise ValueError('download exceeds size limit')
    return data


def release_checksum(text, name):
    matches = [line.split() for line in text.splitlines() if len(line.split()) == 2 and line.split()[1] == name]
    if len(matches) != 1 or not re.fullmatch('[a-fA-F0-9]{64}', matches[0][0]):
        raise ValueError('release checksum missing or ambiguous')
    return matches[0][0].lower()


def download_release(tag, arch, destination):
    if not TAG.fullmatch(tag) or arch not in ('arm64', 'amd64'):
        raise ValueError('unsupported release')
    name = f'touchgrass_{tag[1:]}_linux_{arch}.tar.gz'
    base = f'https://github.com/oralecarlangelo/touchgrass/releases/download/{tag}'
    archive = fetch(f'{base}/{name}', 128 * 1024 * 1024)
    checksum = release_checksum(fetch(f'{base}/checksums.txt', 1024 * 1024).decode(), name)
    if hashlib.sha256(archive).hexdigest() != checksum:
        raise ValueError('release checksum mismatch')
    with tarfile.open(fileobj=io.BytesIO(archive), mode='r:gz') as bundle:
        members = [entry for entry in bundle.getmembers() if entry.name == 'touchgrass']
        if len(members) != 1 or not members[0].isfile() or not 0 < members[0].size <= 256 * 1024 * 1024:
            raise ValueError('release has no regular binary')
        with bundle.extractfile(members[0]) as source, open(destination, 'wb') as target:
            shutil.copyfileobj(source, target)
    destination.chmod(0o755)


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def write_json(path, value, mode=0o644):
    fd, temporary = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as target:
            json.dump(value, target)
            target.flush()
            os.fsync(target.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def health(config):
    body = json.loads(fetch(config['health_url'], 65536, timeout=3))
    if body.get('status') != 'ok' or not VERSION.fullmatch(body.get('version', '')):
        raise ValueError('console health or version is unavailable')
    return body['version']


@contextlib.contextmanager
def lock(path, timeout=0):
    # O_NOFOLLOW prevents a pre-existing symlink lock from redirecting root IO.
    descriptor = os.open(path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        deadline = time.monotonic() + timeout
        while True:
            try:
                fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    raise RuntimeError('another deployment is running')
                time.sleep(0.2)
        yield
    finally:
        os.close(descriptor)


def request(config, tag):
    with lock(STATE / 'request.lock'):
        current = health(config)
        if not is_newer(tag, current):
            raise ValueError('release must be newer than installed base version')
        status_path = STATE / 'status.json'
        if status_path.exists():
            previous = json.loads(status_path.read_text())
            if previous.get('state') in ('pending', 'running'):
                unit = previous.get('unit', '')
                if not re.fullmatch(r'touchgrass-update-[0-9]+\.service', unit):
                    raise RuntimeError('legacy update pending; inspect the existing host job')
                active = subprocess.run(['systemctl', 'is-active', '--quiet', unit], env=ENV, timeout=10)
                if active.returncode == 0:
                    raise RuntimeError('an update is already running')
        unit = f'touchgrass-update-{time.time_ns()}.service'
        status = {'tag': tag, 'state': 'pending', 'started_at': now(), 'unit': unit}
        write_json(status_path, status)
        try:
            command(['systemd-run', '--quiet', '--collect', '--unit=' + unit,
                     '--property=RuntimeMaxSec=10min', '--property=TimeoutStopSec=5min',
                     '/usr/bin/python3', '-I', str(ROOT / 'updater.py'), 'run', tag, current])
        except Exception:
            write_json(status_path, {**status, 'state': 'failed', 'finished_at': now()})
            raise


def backup_database(source, destination):
    if shutil.disk_usage(destination.parent).free < pathlib.Path(source).stat().st_size * 2:
        raise RuntimeError('insufficient space for database backup')
    fd, temporary = tempfile.mkstemp(prefix='.backup-', dir=destination.parent)
    os.close(fd)
    try:
        with contextlib.closing(sqlite3.connect(pathlib.Path(source).as_uri() + '?mode=ro', uri=True)) as db:
            with contextlib.closing(sqlite3.connect(temporary)) as backup:
                db.backup(backup, pages=1000, sleep=0.1)
                if backup.execute('PRAGMA quick_check').fetchone() != ('ok',):
                    raise RuntimeError('database backup failed integrity check')
        os.chmod(temporary, 0o600)
        os.replace(temporary, destination)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def wait_healthy(config, version):
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        try:
            if health(config) == version:
                return
        except Exception:
            pass
        time.sleep(2)
    raise RuntimeError('updated console did not become healthy')


def replace_binary(source):
    # Stage on the destination filesystem so replacement is always atomic.
    temporary = BINARY.with_name('touchgrass.next')
    shutil.copyfile(source, temporary)
    temporary.chmod(0o755)
    os.replace(temporary, BINARY)


def run(config, tag, current):
    status_path = STATE / 'status.json'
    status = json.loads(status_path.read_text())
    if status.get('tag') != tag or status.get('state') != 'pending':
        raise RuntimeError('update request is no longer pending')
    write_json(status_path, {**status, 'state': 'running'})
    replaced = False
    stop_attempted = False
    backup = STATE / ('backup-' + str(time.time_ns()) + '.db')
    previous_binary = backup.with_suffix('.binary')
    try:
        with contextlib.ExitStack() as stack:
            stack.enter_context(lock(STATE / 'install.lock'))
            if config.get('cutover_lock'):
                stack.enter_context(lock(config['cutover_lock'], timeout=120))
            if health(config) != current or not is_newer(tag, current):
                raise RuntimeError('installed version changed before update')
            trusted(BINARY)
            with tempfile.TemporaryDirectory(dir=STATE) as directory:
                work = pathlib.Path(directory)
                download_release(tag, config['arch'], work / 'new')
                backup_database(config['database'], backup)
                shutil.copyfile(BINARY, previous_binary)
                previous_binary.chmod(0o600)
                try:
                    stop_attempted = True
                    command(['systemctl', 'stop', config['service']], timeout=90)
                    replaced = True
                    replace_binary(work / 'new')
                    command(['systemctl', 'start', config['service']], timeout=90)
                    wait_healthy(config, tag[1:])
                except BaseException:
                    if replaced:
                        command(['systemctl', 'stop', config['service']], timeout=90)
                        replace_binary(previous_binary)
                    if stop_attempted:
                        command(['systemctl', 'start', config['service']], timeout=90)
                        wait_healthy(config, current)
                    raise
        write_json(status_path, {**status, 'state': 'succeeded', 'finished_at': now()})
        # Keep three verified updater backups, independent of daily snapshots.
        for old in sorted(STATE.glob('backup-*.db'), key=lambda p: p.stat().st_mtime, reverse=True)[3:]:
            old.unlink()
            old.with_suffix('.binary').unlink(missing_ok=True)
    except BaseException:
        if not stop_attempted:
            backup.unlink(missing_ok=True)
            previous_binary.unlink(missing_ok=True)
        write_json(status_path, {**status, 'state': 'failed', 'finished_at': now()})
        raise


def main():
    if os.geteuid() != 0:
        raise ValueError('updater requires root')
    os.umask(0o077)
    for path in (ROOT / 'updater.py', ROOT / 'config.json', STATE, BINARY):
        trusted(path)
    config = json.loads((ROOT / 'config.json').read_text())
    args = sys.argv[1:]
    if len(args) not in (2, 3) or not TAG.fullmatch(args[1]):
        raise ValueError('expected an exact published release tag')
    signal.signal(signal.SIGTERM, cancel_update)
    if args[0] == 'request' and len(args) == 2:
        request(config, args[1])
    elif args[0] == 'run' and len(args) == 3:
        run(config, args[1], args[2])
    else:
        raise ValueError('unsupported updater operation')


if __name__ == '__main__':
    try:
        main()
    except (Exception, UpdateCancelled) as error:
        # Never print environment, subprocess output, credentials, or DB content.
        print('Touch Grass update failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
