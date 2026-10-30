<?php
// CLI only; output never contains the private connection or service binding.
if (PHP_SAPI !== 'cli') { http_response_code(404); exit; }
define('INSTALL_PATH', '/var/www/html/');
require '/var/www/html/program/include/clisetup.php';
$rc = rcmail::get_instance();
$db = $rc->get_dbh();
foreach (['session','users','identities','contacts'] as $table) {
    if (!$db->query('SELECT 1 FROM ' . $db->table_name($table, true) . ' LIMIT 0') || $db->is_error()) {
        exit(1);
    }
}
exit(0);
