<?php
// Run with the candidate plugin path as argv[1] under PHP; no Roundcube
// service, credentials or browser session is required for this rendering test.
class rcube_plugin {
    public function gettext($key) {
        return ['outboundmissing' => 'Missing <route>', 'outboundunknown' => 'Unknown route',
            'publicunverified' => 'Unverified public mail', 'management' => 'Manage'][$key] ?? $key;
    }
}
require $argv[1];
$plugin = new anas_mail();
$set = function ($name, $value) use ($plugin) {
    $field = new ReflectionProperty($plugin, $name);
    $field->setAccessible(true);
    $field->setValue($plugin, $value);
};
$set('binding', ['go_origin' => 'https://mail.example.test']);
$_SESSION = ['anas_mail' => ['active' => true], 'user_id' => 1];
$original = '<div id="layout-list" class="listbox selected"><div>messages</div></div>';
foreach (['not-configured' => 'Missing &lt;route&gt;', 'unknown' => 'Unknown route', 'unverified' => 'Unverified public mail'] as $status => $label) {
    $set('mailStatus', $status);
    $page = $plugin->render_page(['template' => 'mail', 'content' => $original]);
    if (substr_count($page['content'], 'role="status"') !== 1 || !str_contains($page['content'], $label)
        || !str_contains($page['content'], 'https://mail.example.test/')) {
        fwrite(STDERR, "status rendering failed\n");
        exit(1);
    }
    $other = $plugin->render_page(['template' => 'settings', 'content' => $original]);
    if ($other['content'] !== $original) exit(1);
}
$_SESSION = [];
if ($plugin->render_page(['template' => 'mail', 'content' => $original])['content'] !== $original) exit(1);

// Exercise the same localization merge API used by the pinned Roundcube
// version. Only the send confirmation changes; errors and draft saves remain.
class submission_notice_rc {
    public $texts = ['messagesent' => 'Original success', 'sendingfailed' => 'Original error', 'messagesaved' => 'Original draft'];
    public function read_localization($dir, $language) {
        $labels = [];
        include $dir . '/en_US.inc';
        $english = $labels;
        if ($language && is_file($dir . '/' . $language . '.inc')) {
            include $dir . '/' . $language . '.inc';
        }
        return array_merge($english, $labels);
    }
    public function load_language($language, $add, $merge) {
        if ($add !== [] || array_keys($merge) !== ['messagesent']) throw new Exception('unexpected language override');
        $this->texts = array_merge($this->texts, $merge);
    }
}
$method = new ReflectionMethod($plugin, 'set_submission_confirmation');
$method->setAccessible(true);
foreach (['en_US', 'zh_CN', 'zh_TW', 'de_DE', 'es_ES', 'fr_FR', 'ja_JP', 'ko_KR', 'unavailable'] as $lang) {
    $_SESSION = ['language' => $lang];
    $rc = new submission_notice_rc();
    $expected = $rc->read_localization(dirname($argv[1]) . '/localization/', $lang)['submissionaccepted'];
    $method->invoke($plugin, $rc);
    if ($rc->texts['messagesent'] !== $expected || $rc->texts['messagesent'] === 'Original success'
        || $rc->texts['sendingfailed'] !== 'Original error' || $rc->texts['messagesaved'] !== 'Original draft') {
        fwrite(STDERR, "submission confirmation failed\n");
        exit(1);
    }
}
echo "Roundcube status rendering passed\n";
