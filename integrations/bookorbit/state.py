import hashlib
import json
import os
import tempfile
from pathlib import Path


def key(value):
    return hashlib.sha256(value.encode()).hexdigest()


def file_hash(path):
    path = Path(path)
    if path.is_symlink():
        raise RuntimeError(f'Refusing symlink: {path}')
    if not path.exists():
        return None
    if not path.is_file():
        raise RuntimeError(f'Not a regular file: {path}')
    with path.open('rb') as file:
        return hashlib.file_digest(file, 'sha256').hexdigest()


def atomic_bytes(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix='.serial-sync-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as file:
            file.write(data)
            file.flush()
            os.fsync(file.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


class State:
    def __init__(self, root):
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)

    def path(self, category, identity):
        return self.root / category / (key(identity) + '.json')

    def get(self, category, identity, default=None):
        try:
            return json.loads(self.path(category, identity).read_text())
        except FileNotFoundError:
            return default

    def put(self, category, identity, value):
        atomic_bytes(self.path(category, identity), json.dumps(value, sort_keys=True).encode())

    def remove(self, category, identity):
        self.path(category, identity).unlink(missing_ok=True)

    def pending(self):
        return [json.loads(path.read_text()) for path in sorted((self.root / 'outbox').glob('*.json'))]
