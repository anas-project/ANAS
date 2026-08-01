<?php

declare(strict_types=1);

$names = [
    'LAM_ADMIN_PASSWORD',
    'LAM_LANGUAGE',
    'SAMBA_DC_ADMIN_DN',
    'SAMBA_DC_BASE_COMPUTERS_DN',
    'SAMBA_DC_BASE_DN',
    'SAMBA_DC_BASE_GROUPS_DN',
    'SAMBA_DC_BASE_USERS_DN',
    'SAMBA_DC_DOMAIN',
    'SAMBA_DC_LDAPS_SERVER_URL',
];

$templatePath = '/var/lib/ldap-account-manager/config/lam.conf.envsubst';
$outputPath = '/var/lib/ldap-account-manager/config/lam.conf';
$temporaryPath = $outputPath . '.tmp';

$template = file_get_contents($templatePath);
if ($template === false) {
    fwrite(STDERR, "cannot read LAM configuration template\n");
    exit(1);
}

$replacements = [];
foreach ($names as $name) {
    $value = getenv($name);
    if ($value === false) {
        fwrite(STDERR, "missing required environment variable: {$name}\n");
        exit(1);
    }
    $replacements['${' . $name . '}'] = $value;
}

$rendered = strtr($template, $replacements);
if (preg_match('/\$\{[A-Z][A-Z0-9_]*\}/', $rendered, $match) === 1) {
    fwrite(STDERR, "unresolved variable in LAM configuration: {$match[0]}\n");
    exit(1);
}

if (file_put_contents($temporaryPath, $rendered, LOCK_EX) === false) {
    fwrite(STDERR, "cannot write LAM configuration\n");
    exit(1);
}
if (!rename($temporaryPath, $outputPath)) {
    fwrite(STDERR, "cannot install LAM configuration\n");
    exit(1);
}
