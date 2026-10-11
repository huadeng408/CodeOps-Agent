#!/bin/sh
set -eu
exec /opt/opensandbox/execd --init -- "$@"
