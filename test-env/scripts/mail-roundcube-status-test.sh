#!/bin/sh
# TEST_CASES: MAILMOD-T-022
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
exec php "$root/modules/mail/roundcube/tests/status.php" "$root/modules/mail/roundcube/plugins/anas_mail/anas_mail.php"
