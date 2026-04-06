# Patreon Concurrency Experiment

## Question

Should `serial-sync` fetch Patreon post details concurrently during live sync, or is the concurrency causing enough `429` responses that the default should be serial or nearly serial?

## Method

I used the new live run instrumentation from April 5, 2026 against the real `plum-parrot` source with env-only Patreon auth and a fresh runtime workspace:

- config: `/tmp/serial-sync-live-check-bl5T3t/config.toml`
- session bundle: `/tmp/serial-sync-live-check-bl5T3t/state/sessions/patreon-default.json`
- run id: `run_954a3b81-feb8-48a8-8953-14bf80128e0d`

I did not run a second matrix of live experiments at different worker counts because the account was already hitting rate limits hard enough that back-to-back comparison runs would mostly measure cooldown behavior instead of steady fetch capacity. The recommendation below is based on the completed instrumented run plus the product constraint that initial backfills can be slower, while steady-state incremental runs should be reliable and cheap.

## Observed Behavior

Feed pagination was not the problem.

- `29` feed pages fetched
- `572` post ids discovered
- feed pagination duration: `30.4s`

The detail-fetch phase was the problem.

- detail fetch started immediately after feed pagination
- first `429` arrived on the first retry attempt with `Retry-After: 60`
- worker limit in this run: `4`
- total detail-progress checkpoints emitted: `23`
- total `429` backoff events emitted: `344`
- total post-detail failures emitted: `81`
- run finished failed after about `4m11s`

The important shape is:

1. the feed scan completed cleanly
2. detail fetch progressed quickly at first
3. after enough concurrent detail requests accumulated, the provider entered a heavy `429` regime
4. retries/backoff dominated the rest of the run and the run still failed

## Interpretation

Concurrency is still useful in principle, but `4` is too aggressive as the default for Patreon detail fetches on a cold full-history sync.

Why I would not drop concurrency entirely:

- steady-state incremental runs are supposed to fetch a much smaller delta, so a small amount of concurrency can still reduce wall-clock time
- the expensive phase is detail fetch, not feed pagination, and serial detail fetch across a large backlog may be unnecessarily slow once rate limiting is not the dominant constraint

Why I would not keep `4` as the default:

- the real run produced hundreds of `429` backoff events
- once Patreon starts returning `Retry-After: 60`, more parallelism stops helping
- this product does not require first-run speed if that speed materially reduces reliability

## Recommendation

Use a much lower default and treat concurrency as a steady-state optimization, not a first-backfill optimization.

Recommended default:

- full-history / no stored cursor: `1`
- incremental / stored cursor present: `2`

If you want one fixed default instead of mode-specific behavior:

- pick `1`

That is the safer product default because:

- first sync can be slow
- incremental runs are where speed matters
- incremental runs should become fast anyway once the live cursor is working correctly

## Follow-up Improvement

The best version is adaptive rather than fixed:

1. start at `2` only when a stored cursor exists
2. drop to `1` immediately after the first `429`
3. keep `1` for the rest of that run

That keeps steady-state runs reasonably quick without turning first backfills into retry storms.
