#!/bin/sh
set -eu
mkdir -p releases
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  target_os=${target%/*}
  target_arch=${target#*/}
  target_ext=
  if [ "$target_os" = windows ]; then target_ext=.exe; fi
  echo "Building $target"
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -buildvcs=false -trimpath -ldflags='-s -w' -o "releases/podport-$target_os-$target_arch$target_ext" .
done
