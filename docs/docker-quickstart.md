# Docker Quickstart

Build the image:

```sh
docker build -t serial-sync .
```

Run the demo config:

```sh
docker run --rm -it \
  -v "$PWD/examples:/app/examples" \
  -v "$PWD/testdata:/app/testdata" \
  -v "$PWD/state:/app/state" \
  -v "$PWD/publish:/app/publish" \
  serial-sync \
  --config /app/examples/config.demo.toml sync
```

Use the same image for `plan sync`, `publish`, and inspect commands.
