#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
GO111MODULE=on go run .
