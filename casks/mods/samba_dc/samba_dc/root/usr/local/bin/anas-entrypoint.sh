#!/usr/bin/env bash
set -euo pipefail

for init_script in /etc/cont-init.d/*.sh; do
  "$init_script"
done

exec runsvdir -P /etc/services.d
