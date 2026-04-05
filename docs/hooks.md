# Hook Tutorial

The current MVP reserves the `exec` publisher seam but only ships `filesystem`.

The intended model is:

1. `sync` materializes canonical artifacts into durable local storage.
2. `publish` replays those artifacts to one or more targets.
3. A future `exec` publisher will receive stable file paths and metadata sidecars.

For now, use the filesystem publisher and layer your own automation on top of the published directory.
