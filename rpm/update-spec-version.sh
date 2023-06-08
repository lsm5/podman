#!/usr/bin/env bash

# This script will update the Version field in the spec which is set to 0 by
# default. Useful for local manual rpm builds where the Version needs to be set
# correctly.

set -eo pipefail

PACKAGE=podman

# Script is run from git root directory
SPEC_FILE=rpm/$PACKAGE.spec

LATEST_TAG=$(git describe)
LATEST_VERSION=$(echo $LATEST_TAG | sed -e 's/^v//' -e 's/-/~/g')

git archive --prefix=$PACKAGE-$VERSION/ -o $PACKAGE-$VERSION.tar.gz HEAD

sed -i "s/^Version:.*/Version: $LATEST_VERSION/" $SPEC_FILE
