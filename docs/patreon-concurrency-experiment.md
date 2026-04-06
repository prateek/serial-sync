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

## Follow-up Validation

After the adaptive-budget experiment, I reran the same live `plum-parrot` path with the request budget hard-capped to a single in-flight Patreon request:

- fresh runtime workspace: `/tmp/serial-sync-live-e2e-serial.U9xwjM`
- `setup auth`: succeeded
- first `run sync`: succeeded in `3m56s`
- first `run publish`: succeeded in `0.19s`
- immediate second `run sync`: succeeded in `10.9s`
- immediate second `run publish`: succeeded in `0.006s`

The first full-history run completed without the earlier `429` storm:

- `572` posts discovered
- `572` releases classified
- `25` artifacts materialized
- `25` artifacts published
- no detail-fetch failures

The immediate second run stayed incremental:

- `25` posts discovered
- `0` changed
- `25` unchanged
- `0` artifacts materialized
- `0` artifacts published

## Interpretation

For Patreon, the important product requirement is reliability, not maximizing cold-sync throughput.

The validated behavior is:

- a single in-flight request is reliable for full-history fetches
- incremental runs are already fast enough without concurrency
- the earlier adaptive policy still climbed back into a bad `429` regime on real traffic

So the question is no longer theoretical. We have an end-to-end live result showing that the serialized client works acceptably for both:

- first-run backfills
- steady-state incremental syncs

## Recommendation

Keep the Patreon live client serialized for now:

- initial request budget: `1`
- minimum request budget: `1`
- maximum request budget: `1`

That is intentionally conservative, but it matches the behavior we have actually validated against the live source.

If we revisit higher concurrency later, it should only happen behind another real live validation pass with explicit pacing/cooldown controls, not by re-enabling optimistic budget growth by default.
