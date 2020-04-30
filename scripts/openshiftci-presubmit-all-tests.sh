#!/bin/sh

# fail if some commands fails
set -e
# show commands
set -x

INITIAL_PWD="$(pwd)"

export CI="openshift"
make configure-installer-tests-cluster
make bin
mkdir -p $GOPATH/bin
go get -u github.com/onsi/ginkgo/ginkgo
export PATH="$PATH:$(pwd):$GOPATH/bin"
export ARTIFACTS_DIR="/tmp/artifacts"
export CUSTOM_HOMEDIR=$ARTIFACTS_DIR

# Download latest stable kubectl to '/tmp/kubectl' and append to PATH
mkdir -p  "$INITIAL_PWD/kubectl"
curl -L -o "$INITIAL_PWD/kubectl/kubectl" https://storage.googleapis.com/kubernetes-release/release/`curl -s https://storage.googleapis.com/kubernetes-release/release/stable.txt`/bin/linux/amd64/kubectl
chmod +x "$INITIAL_PWD/kubectl/kubectl"
export PATH="$PATH:$INITIAL_PWD/kubectl"


# Integration tests
make test-integration
make test-integration-devfile
make test-cmd-login-logout
make test-cmd-project
make test-operator-hub

# E2e tests
make test-e2e-all

odo logout
