"""The shared OS lock owns execution; journal failures cannot stop scheduled sync."""
import fcntl
import signal
import sqlite3
import subprocess
import sys
import time

from serial_adapter import RUN_ID, summarize


class RunLock:
    def __init__(self, path, timeout=0):
        self.path = path
        self.timeout = timeout
        self.file = None

    def __enter__(self):
        self.file = open(self.path, 'a')
        deadline = time.monotonic() + self.timeout
        while True:
            try:
                fcntl.flock(self.file, fcntl.LOCK_EX | fcntl.LOCK_NB)
                return self
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    self.file.close()
                    self.file = None
                    return None
                time.sleep(0.025)

    def __exit__(self, *_args):
        if self.file:
            self.file.close()


def journal(call):
    try:
        return call()
    except (sqlite3.Error, OSError):
        print('[runner.journal] [fail] errorClass=JournalUnavailable error="journal unavailable" - scheduled execution remains independent', file=sys.stderr, flush=True)
        return None


def execute(store, command, catalog, logs, origin, lock_path, request_key=None):
    with RunLock(lock_path, timeout=1) as lock:
        if lock is None:
            if store:
                journal(lambda: store.lock_busy(origin, request_key))
            return 75
        if origin == 'manual':
            # A manual request must retain durable deduplication, even during a journal outage.
            if not store or not request_key:
                return 1
            run_id = store.claim(origin, request_key)
            if run_id is None:
                return 0
        else:
            run_id = journal(lambda: store.claim(origin)) if store else None
        return run_command(store, run_id, command, catalog, logs, lock.file.fileno())


def run_command(store, run_id, command, catalog, logs, lock_fd):
    children = []
    interrupted = False
    process = None

    def terminate(_signal, _frame):
        nonlocal interrupted
        interrupted = True
        if process is not None:
            process.terminate()

    old_term = signal.signal(signal.SIGTERM, terminate)
    old_int = signal.signal(signal.SIGINT, terminate)
    try:
        if interrupted:
            code = 143
        else:
            process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=None, text=True, pass_fds=(lock_fd,))
            if interrupted:
                process.terminate()
            with process.stdout as stream:
                for line in stream:
                    sys.stdout.write(line)
                    sys.stdout.flush()
                    match = RUN_ID.fullmatch(line)
                    if match and len(children) < 100:
                        children.append(match[1])
            code = process.wait()
        counters, verified = {}, []
        if children:
            try:
                counters, verified = summarize(catalog, logs, children)
            except (OSError, ValueError, sqlite3.Error):
                counters = {'unavailableSummaries': 1}
        state = 'interrupted' if interrupted else 'succeeded' if code == 0 else 'busy' if code == 75 else 'failed'
        message = {
            'succeeded': 'Execution completed. Counters describe events, not unique artifacts. BookOrbit import is unconfirmed.',
            'busy': 'Shared maintenance lock was held; no sync ran.',
            'failed': 'Execution failed. Inspect the serial-sync service journal; event counters may not include all failures.',
            'interrupted': 'Execution was interrupted; inspect the service and container before retrying.',
        }[state]
        if run_id:
            journal(lambda: store.finish(run_id, state, message, counters, verified))
        return 143 if interrupted else code
    except BaseException:
        if run_id:
            journal(lambda: store.finish(run_id, 'interrupted', 'Execution ended without a verified completion.'))
        raise
    finally:
        signal.signal(signal.SIGTERM, old_term)
        signal.signal(signal.SIGINT, old_int)
