#!/bin/bash

VERSION="0.0.6"
LATEST="latest"

# if branch is unstable in git for circle ci
if [ -n "$CIRCLE_BRANCH" ]; then
  if [ "$CIRCLE_BRANCH" != "master" ]; then
    LATEST="$LATEST-$CIRCLE_BRANCH"
  fi
fi

echo "Pushing azukaar/docker-self-updater:$VERSION and azukaar/docker-self-updater:$LATEST"

sh build.sh

docker build \
  -t azukaar/docker-self-updater:$VERSION \
  -t azukaar/docker-self-updater:$LATEST \
  .

docker push azukaar/docker-self-updater:$VERSION
docker push azukaar/docker-self-updater:$LATEST