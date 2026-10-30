#!/bin/sh
set -eu
binding_input=/run/anas/mail-webmail-input/config.json
if [ -e "$binding_input" ]; then
  test -f "$binding_input"
  test ! -L "$binding_input"
  test "$(stat -c '%a %h' "$binding_input")" = '600 1'
  install -d -m 0700 -o www-data -g www-data /run/anas/mail-webmail
  install -m 0600 -o www-data -g www-data "$binding_input" /run/anas/mail-webmail/config.json
fi
# Isolated fixtures may mount an already-scoped binding at the runtime path.
test -f /run/anas/mail-webmail/config.json
