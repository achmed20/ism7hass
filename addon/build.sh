#!/bin/sh
# Copies the Go source next to the Dockerfile (the host builds with addon/ as context).
# Then copy the addon/ folder to /addons/ism7 on the Home Assistant host.
set -e
cd "$(dirname "$0")/.."
rm -rf addon/src && mkdir addon/src
cp -r go.mod go.sum main.go cmd internal version addon/src/
echo "ready: copy addon/ to /addons/ism7"
