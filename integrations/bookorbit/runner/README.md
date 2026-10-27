# Serial-sync runner bridge

This Python 3.11+ service implements [BookOrbit's backend-neutral runner protocol](https://github.com/prateek/bookorbit/blob/downstream/docs/EXTERNAL_RUNNERS.md). It exposes a configured sync job, durable execution history, sanitized lifecycle events and run-now control. Source credentials, schedules, configuration, EPUB generation and file delivery remain in serial-sync. There are no new Python dependencies.

The generic BookOrbit feature lives separately in BookOrbit. Only this adapter understands serial-sync's catalog, event kinds, CLI output, Docker command and systemd units. A FanFicFare adapter can implement the same HTTP contract without importing this code.

## Execution ownership

The wrapper acquires the existing `run.lock` using `fcntl.flock` before claiming an execution. It holds the lock until completion, and the child process inherits its descriptor. Other maintenance commands using `flock(1)` remain mutually exclusive. Do not wrap the command in another `flock` for the same file: the wrapper now owns that lock.

Scheduled and external runs continue if the monitoring journal cannot be opened or written. A stale running receipt is reconciled while holding the lock, without needing the HTTP service. Manual runs require a durable request key and fail closed if the journal is unavailable, preserving deduplication. A failed HTTP/systemd start response retains its queued reservation because systemd may have accepted it.

Manual requests start `serial-sync-manual@<request-key>.service`. The instance claims only that request; the hourly unit always creates its own scheduled execution. If they race, the lock winner runs and the other records `busy` with exit 75. A retry of a completed request cannot start another execution.

Reconciliation briefly probes `run.lock` after reading candidates and uses conditional state updates, so it cannot mark a live wrapped execution interrupted, including a command launched outside systemd. It holds no execution lock while waiting on SQLite or systemd. The wrapper retries transient lock contention for up to one second before recording busy. Queued requests get a two-minute startup grace and are reconciled against their exact manual instance. The bridge checks every 30 seconds even when BookOrbit is closed.

Cancellation is unavailable. Killing the Docker client does not prove the container stopped safely. After interruption, inspect both the systemd unit and the fixed-name container before retrying. All invocations retain the same container name as an additional guard against overlapping containers after a service kill.

## Install

Prepare the files before changing an active deployment. Installation starts no sync, but the next scheduled tick uses the wrapper.

1. Copy `runner.py`, `execution.py`, `store.py` and `serial_adapter.py` to `~/serial-sync-runner/`. Keep them versioned together with the serial-sync release.
2. Copy `runner.example.toml` to `~/serial-sync/config/runner.toml`. Adjust absolute paths, `unit`, `manual_unit`, `lock_path` and schedule description. Catalog and logs must use real host paths and be readable by the service user, including SQLite WAL sidecars. Back up the private `runner-state` directory.
3. Generate a bearer token at `token_file`, mode 0600, for example `umask 077; openssl rand -hex 32 > ~/serial-sync/config/runner-token`.
4. Before adding the execution override, snapshot the effective scheduled unit as the manual template: `systemctl --user cat serial-sync.service > ~/.config/systemd/user/serial-sync-manual@.service`. This preserves all existing sections and drop-ins, including capacity `ExecStartPre`, working directory and timeouts. Review the resulting file. When the base unit or its capacity policy changes later, refresh this template too.
5. Install `execution.conf` under `~/.config/systemd/user/serial-sync.service.d/`. Install `manual-execution.conf` under `~/.config/systemd/user/serial-sync-manual@.service.d/`. The supplied commands match the one-shot Compose deployment; adjust both if yours differs. Keep the same fixed container name. Install `serial-sync-runner.service` under `~/.config/systemd/user/`.
6. Inspect both effective units with `systemctl --user cat`. Verify preserved capacity checks and timeout settings, a single wrapper command, and no nested `flock`. Run `systemd-analyze --user verify` on the unit files. The deployment reconciler must preserve these drop-ins. Run `systemctl --user daemon-reload` and `systemctl --user enable --now serial-sync-runner.service`. Keep the existing hourly timer.
7. Expose the runner only to BookOrbit's backend through HTTPS or a trusted private interface/firewall rule. Loopback is not reachable from a normal Docker container. For a private Docker gateway, set `listen_host` to that address explicitly and use the literal private IP with BookOrbit's private-network opt-in. HTTP hostnames and public IPs are rejected by BookOrbit. Never expose plaintext bearer authentication publicly.
8. Configure BookOrbit's `EXTERNAL_RUNNER_ENCRYPTION_KEY`, apply its migration and add the URL/token under Tools > External tools. Read capabilities/history first. Run a staging fixture before requesting a production sync.

The token is separate from Patreon cookies and BookOrbit login credentials. This bridge exposes all runs of its single configured sync service to its token holder. Use separately scoped instances/tokens where different users need different access.

## Results and operations

One parent execution attaches only child run IDs explicitly emitted by the CLI and found in the catalog. History before installation is not paired by timestamp. While executing, the UI shows state; counters are available after completion.

Counters are **event counts**, not unique artifact counts or complete failure totals. `heldEvents`, `unchangedEvents`, `publishedEvents` and `publishFailureEvents` correspond to those JSONL event kinds. Resumed plans may emit events again. Plan blocks, lifecycle and retirement failures may not emit a publish-failure event; the process exit and terminal run state remain authoritative. Missing summaries are explicit. Successful execution never confirms BookOrbit import.

Raw stdout is flushed to the service journal as it arrives and is never served to BookOrbit. The bridge serves only bounded lifecycle messages, pages and counters. For detailed diagnostics use the service journal and `serial-sync debug`.

For an operator invocation outside systemd, use `--origin external` before `execute`, with the fixed Docker command after `--`. The wrapper takes `lock_path` itself. Prefer the systemd unit, which also runs deployment capacity checks. Unwrapped runs are not tracked.

Back up `runner-state/runner.db` with its WAL consistently while idle, or use SQLite's backup API. Restore the stable instance ID and deduplication journal together. History retention is an operator policy.

To disable the integration, stop the bridge, remove the scheduled execution drop-in and the manual template/drop-in, then reload systemd. Keep the journal. The original scheduled command and file delivery continue.

## Verification

Run `python3 -m unittest discover -s integrations/bookorbit/runner -v`. CI runs this suite alongside Go tests. It covers HTTP authentication and failures, durable retries, real file-lock contention, stale recovery without the bridge, journal outage, external-run liveness, timer/manual attribution, parent/child receipts and event counts. Unit tests use temporary data and small subprocesses; they do not contact Patreon.

Before production activation, also check Python >= 3.11, the effective units, same-user catalog/WAL readability, and a disposable systemd fixture run including stop/timeout handling. Release publication alone does not install the host-side bridge.
