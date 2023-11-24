#!/bin/bash

VERSION="0.0.10"
LATEST="latest"

# if branch is unstable in git for circle ci
if [ -n "$CIRCLE_BRANCH" ]; then
  if [ "$CIRCLE_BRANCH" != "master" ]; then
    LATEST="$LATEST-$CIRCLE_BRANCH"
  fi
fi

echo "Pushing azukaar/docker-self-updater:$VERSION and azukaar/docker-self-updater:$LATEST"

sh build.arm64.sh

docker build \
  -t azukaar/docker-self-updater:$VERSION-arm64 \
  -t azukaar/docker-self-updater:$LATEST-arm64 \
  -f dockerfile.arm64 \
  --platform linux/arm64 \
  .

docker push azukaar/docker-self-updater:$VERSION-arm64
docker push azukaar/docker-self-updater:$LATEST-arm64