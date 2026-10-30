#!/bin/sh
set -eu
/usr/local/bin/anas-mail-dms prepare
exec "$@"
