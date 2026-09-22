#!/usr/bin/env bash
set -euo pipefail
reader_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
build_dir=${1:?Usage: build.sh BUILD_DIRECTORY [IMAGE_TAG] [PLATFORM]}
image_tag=${2:-serial-reader:3.0.0-1}
platform=${3:-linux/amd64}
revision=6be648b48a9cc376cfeff8953ebf22123583f7eb
mkdir -p "$build_dir"
build_dir=$(cd "$build_dir" && pwd)
if [[ -e "$build_dir/source" ]]; then
  echo 'Use an empty build directory to avoid overwriting a checkout.' >&2
  exit 1
fi
git clone --filter=blob:none https://github.com/bookorbit/bookorbit.git "$build_dir/source"
git -C "$build_dir/source" checkout --detach "$revision"
git -C "$build_dir/source" apply --check "$reader_dir/reader.patch"
git -C "$build_dir/source" apply "$reader_dir/reader.patch"
cp -R "$reader_dir" "$build_dir/source/serial-sync-integration"
# JavaScript output is portable; native dependencies come from the pinned target image.
docker build --target server-builder -t serial-sync-reader-server-build "$build_dir/source"
docker build --target client-builder -t serial-sync-reader-client-build "$build_dir/source"
for component in server client; do
  container=$(docker create "serial-sync-reader-${component}-build")
  docker cp "$container:/app/$component/dist" "$build_dir/$component-dist"
  docker rm "$container" >/dev/null
done
tar --exclude=.git -czf "$build_dir/serial-sync-source.tar.gz" -C "$build_dir" source
cp "$reader_dir/Dockerfile" "$build_dir/Dockerfile"
docker build --platform "$platform" -t "$image_tag" "$build_dir"
