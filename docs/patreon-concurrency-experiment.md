# Patreon Concurrency Experiment

## Question

At what in-flight Patreon detail-request count do live `plum-parrot` syncs start to trip real `429` rate limits, and which request-budget default gives the best end-to-end behavior?

## Method

I ran a live matrix against the same real `plum-parrot` Patreon source using env-only auth, fresh runtime state for each cold sync, and the new provider progress instrumentation.

The comparison points were:

- serialized baseline `1 -> 1`
- fixed `2 -> 2`
- fixed `3 -> 3`
- adaptive `2 -> 4`

The meaningful live workspaces were:

- `1 -> 1`: `/tmp/serial-sync-live-e2e-serial.U9xwjM`
- `2 -> 2`: `/tmp/serial-sync-live-e2e-fixed2.Zi1HJX`
- `3 -> 3`: `/tmp/serial-sync-live-e2e-fixed3.iGtOwT`
- adaptive `2 -> 4`: `/tmp/serial-sync-live-e2e-adaptive.NOOJIO`

## Results

### `1 -> 1`

- full sync succeeded
- sync duration: about `3m56s`
- `429` events: `0`
- immediate incremental rerun: about `10.9s`

### `2 -> 2`

- full sync succeeded
- sync duration: about `2m23s`
- `429` events: `0`

This is the fastest clean run in the matrix.

### `3 -> 3`

- full sync succeeded
- sync duration: about `2m32s`
- `429` events: `1`
- Patreon returned `Retry-After: 60`

The exact `429` payload shows the request was rate-limited while there were `3` detail requests in flight:

```json
{
  "budget": {
    "limit": 2,
    "in_flight": 3
  },
  "delay_ms": 60000,
  "retry_after": "60",
  "status": 429
}
```

Important nuance: the snapshot is captured after the budget reduction, so the run had already reached `3` in flight when Patreon pushed back.

### Adaptive `2 -> 4`

- full sync succeeded
- sync duration: about `4m43s`
- `429` events: `3`
- each `429` carried `Retry-After: 60`

The first recorded `429` happened after the client had already ramped up to `4` in-flight detail requests.

## Interpretation

For this source and account, the boundary is clear enough:

- `2` concurrent detail requests completed cleanly
- `3` concurrent detail requests can already trigger real `429`s
- `4` concurrent detail requests make the rate limiting materially worse

So the answer is:

- no, we did not see rate limiting at `2`
- yes, we did see rate limiting at `3`
- and `4` is clearly worse than `3`

`3 -> 3` still completed, but it only did so by paying a full one-minute server cooldown. That made it slower than `2 -> 2`, even though its pre-rate-limit fetch speed was higher.

## Recommendation

Use a fixed Patreon request budget of:

- initial request budget: `2`
- minimum request budget: `1`
- maximum request budget: `2`

This is the highest clean setting validated so far. It improves cold full-history sync time significantly over `1 -> 1` without entering the `429` regime that starts at `3`.

If we revisit higher concurrency later, it should be behind another explicit live experiment, not as an adaptive default.
