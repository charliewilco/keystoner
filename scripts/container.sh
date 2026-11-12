#!/bin/sh
# Apple container lifecycle for the local API. No Docker daemon required.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
image=${KEYSTONER_IMAGE:-keystoner:local}
name=${KEYSTONER_CONTAINER:-keystoner-local}
port=${KEYSTONER_HOST_PORT:-8080}
arch=${KEYSTONER_ARCH:-arm64}
case "$arch" in arm64|amd64) ;; *) echo "KEYSTONER_ARCH must be arm64 or amd64" >&2; exit 2 ;; esac
command -v container >/dev/null 2>&1 || {
  echo 'Install Apple container first: https://github.com/apple/container' >&2
  exit 1
}
case "${1:-}" in
  build)
    cd "$root"
    mkdir -p work/container
    stage=$(mktemp -d "$root/work/container/build.XXXXXX")
    trap 'rm -rf "$stage"' EXIT HUP INT TERM
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "${GO:-go}" build -trimpath -ldflags='-s -w' -o "$stage/keystoner" ./cmd/keystoner
    cp containers/Containerfile "$stage/Containerfile"
    cp -R catalog "$stage/catalog"
    container build --platform "linux/$arch" -t "$image" "$stage"
    ;;
  run)
    exec container run --detach --name "$name" --read-only --publish "127.0.0.1:$port:8080" "$image"
    ;;
  stop)
    container stop "$name"
    container delete "$name"
    ;;
  logs)
    exec container logs "$name"
    ;;
  *)
    echo "Usage: $0 {build|run|stop|logs}" >&2
    exit 2
    ;;
esac
