<?php
/** Thin Roundcube adapter. Go owns personnel, mailbox and address authorization. */
class anas_mail extends rcube_plugin
{
    private $binding;
    private $credential;
    private $lock;
    private $logoutCredential;
    private $mailStatus;

    public function init()
    {
        // Upstream initdb loads plugins before creating an HTTP session.
        // It must be able to install/upgrade SQL without browser-only hooks.
        if (PHP_SAPI === 'cli') {
            return;
        }
        require_once __DIR__ . '/directory.php';
        $this->add_texts('localization/', true);
        $this->binding = rcmail::get_instance()->config->get('anas_mail');
        if (!is_array($this->binding)) {
            $this->fail(503);
        }
        $origin = parse_url($this->binding['public_origin']);
        $host = $origin['host'] . (isset($origin['port']) ? ':' . $origin['port'] : '');
        if (($_SERVER['HTTP_HOST'] ?? '') !== $host) {
            $this->fail(401);
        }
        foreach (['startup', 'authenticate', 'storage_connect', 'smtp_connect', 'user_create', 'login_after', 'logout_after', 'unauthenticated', 'identity_update'] as $hook) {
            $this->add_hook($hook, [$this, $hook]);
        }
        $this->add_hook('identity_create', [$this, 'deny_identity_change']);
        $this->add_hook('identity_delete', [$this, 'deny_identity_change']);
        $this->add_hook('template_object_username', [$this, 'display_username']);
        $this->add_hook('addressbooks_list', [$this, 'address_sources']);
        $this->add_hook('addressbook_get', [$this, 'addressbook_get']);
        $this->add_hook('render_page', [$this, 'render_page']);
        $this->include_stylesheet('mail-status.css');
        rcmail::get_instance()->config->set('autocomplete_addressbooks', !empty($this->binding['company_directory']) ? ['sql','anas_company'] : ['sql']);
    }

    private function fail($status)
    {
        $rc = rcmail::get_instance();
        if ($status === 401 && $rc->session) {
            $rc->kill_session();
            // Do not recreate a deleted session from an older concurrent request.
            $rc->session->nowrite = true;
        }
        http_response_code($status);
        header('Content-Type: text/plain; charset=utf-8');
        header('Cache-Control: no-store');
        header('Referrer-Policy: no-referrer');
        exit($this->gettext($status === 401 ? 'sessionended' : 'unavailable'));
    }

    private function serialize_session()
    {
        $rc = rcmail::get_instance();
        $dir = $rc->config->get('temp_dir') . '/anas-session-locks';
        if (!is_dir($dir) && !@mkdir($dir, 0700) && !is_dir($dir)) {
            $this->fail(503);
        }
        // A bounded set avoids an unbounded file per expired browser session.
        $bucket = substr(hash('sha256', session_id()), 0, 2);
        $this->lock = @fopen($dir . '/' . $bucket . '.lock', 'c');
        if (!$this->lock) {
            $this->fail(503);
        }
        $deadline = microtime(true) + 5;
        while (!flock($this->lock, LOCK_EX | LOCK_NB)) {
            if (microtime(true) >= $deadline) {
                $this->fail(503);
            }
            usleep(10000);
        }
        register_shutdown_function(function () use ($rc) {
            $rc->session->write_close();
            flock($this->lock, LOCK_UN);
            fclose($this->lock);
        });
        $required = !empty($_SESSION['user_id']) || !empty($_SESSION['anas_mail']) || !empty($_SESSION['anas_handoff']);
        $data = $rc->session->read(session_id());
        if ($data === '' || $data === false) {
            if ($required) {
                $this->fail(401);
            }
        } else {
            // reload() keeps stale variables when the row disappears. Replace
            // the complete snapshot only after checking a fresh database read.
            $_SESSION = [];
            if (!session_decode($data)) {
                $this->fail(401);
            }
        }
    }

    private function form($fields)
    {
        if (($_SERVER['REQUEST_METHOD'] ?? '') !== 'POST'
            || ($_SERVER['HTTP_ORIGIN'] ?? '') !== $this->binding['go_origin']
            || !empty($_SERVER['HTTP_AUTHORIZATION'])
            || strtolower(trim(explode(';', $_SERVER['CONTENT_TYPE'] ?? '')[0])) !== 'application/x-www-form-urlencoded'
            || (int) ($_SERVER['CONTENT_LENGTH'] ?? 0) > 2048) {
            $this->fail(401);
        }
        // PHP normally collapses duplicate fields; parse the small raw form to
        // reject them, unknown keys and array syntax before using any value.
        $raw = file_get_contents('php://input', false, null, 0, 2049);
        if ($raw === false || strlen($raw) > 2048) {
            $this->fail(401);
        }
        $form = [];
        foreach (explode('&', $raw) as $pair) {
            $parts = explode('=', $pair, 2);
            $key = urldecode($parts[0]);
            $value = urldecode($parts[1] ?? '');
            if (!in_array($key, $fields, true) || array_key_exists($key, $form) || $value === '') {
                $this->fail(401);
            }
            $form[$key] = $value;
        }
        if (count($form) !== count($fields)) {
            $this->fail(401);
        }
        return $form;
    }

    private function post_to_go($path, $fields)
    {
        $nonce = base64_encode(random_bytes(32));
        header('Content-Type: text/html; charset=utf-8');
        header('Cache-Control: no-store');
        // no-referrer makes cross-origin navigation POSTs send Origin: null.
        // Keep strict Origin verification while exposing no path or query.
        header('Referrer-Policy: origin');
        header("Content-Security-Policy: default-src 'none'; script-src 'nonce-{$nonce}'; form-action " . $this->binding['go_origin'] . "; frame-ancestors 'none'; base-uri 'none'");
        echo '<!doctype html><html><head><meta charset="utf-8"><title>Mail</title></head><body><form id="handoff" method="post" action="'
            . htmlspecialchars($this->binding['go_origin'] . $path, ENT_QUOTES, 'UTF-8') . '">';
        foreach ($fields as $key => $value) {
            echo '<input type="hidden" name="' . htmlspecialchars($key, ENT_QUOTES, 'UTF-8') . '" value="' . htmlspecialchars($value, ENT_QUOTES, 'UTF-8') . '">';
        }
        echo '<button type="submit">' . htmlspecialchars($this->gettext('continue'), ENT_QUOTES, 'UTF-8') . '</button></form><script nonce="' . $nonce . '">document.getElementById("handoff").submit();</script></body></html>';
        exit;
    }

    private function api($operation, $fields, $includeStatus = false)
    {
        $paths = ['redeem'=>'/internal/webmail/redeem','refresh'=>'/internal/webmail/refresh','session'=>'/internal/webmail/session','logout'=>'/internal/webmail/logout',
            'directory-sources'=>'/internal/directory/sources','directory-search'=>'/internal/directory/search','directory-groups'=>'/internal/directory/groups','directory-contact'=>'/internal/directory/contact'];
        if (!isset($paths[$operation])) {
            $this->fail(503);
        }
        $max_size = str_starts_with($operation, 'directory-') ? 9 * 1048576 : 32768;
        $curl = curl_init($this->binding['internal_url'] . $paths[$operation]);
        $body = '';
        $headers = ['Authorization: Bearer ' . $this->binding['service_token'], 'Accept: application/json'];
        if ($includeStatus && $operation === 'session') {
            $headers[] = 'X-ANAS-Mail-Status: 1';
        }
        curl_setopt_array($curl, [
            CURLOPT_POST => true, CURLOPT_POSTFIELDS => http_build_query($fields, '', '&', PHP_QUERY_RFC3986),
            CURLOPT_HTTPHEADER => $headers,
            CURLOPT_CONNECTTIMEOUT => 3, CURLOPT_TIMEOUT => 12, CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTPS, CURLOPT_SSL_VERIFYPEER => true, CURLOPT_SSL_VERIFYHOST => 2,
            CURLOPT_CAINFO => $this->binding['ca_file'],
            CURLOPT_WRITEFUNCTION => function ($curl, $chunk) use (&$body, $max_size) {
                if (strlen($body) + strlen($chunk) > $max_size) {
                    return 0;
                }
                $body .= $chunk;
                return strlen($chunk);
            },
        ]);
        $ok = curl_exec($curl);
        $status = curl_getinfo($curl, CURLINFO_HTTP_CODE);
        curl_close($curl);
        if ($ok === false || $status !== 200) {
            $this->fail($status === 401 ? 401 : 503);
        }
        $result = json_decode($body, true);
        if (!is_array($result)) {
            $this->fail(503);
        }
        return $result;
    }

    public function directory_api($operation, $fields = [])
    {
        if (empty($this->binding['company_directory']) || empty($_SESSION['anas_mail']) || empty($_SESSION['user_id'])) {
            $this->fail(401);
        }
        $fixed = ['refresh_token' => rcmail::get_instance()->decrypt($_SESSION['anas_mail']['refresh_token'])];
        if ($operation !== 'sources') {
            $fixed['source'] = 'company';
        }
        return $this->api('directory-' . $operation, $fixed + $fields);
    }

    public function address_sources($args)
    {
        if (empty($this->binding['company_directory']) || empty($_SESSION['anas_mail']) || empty($_SESSION['user_id'])) {
            return $args;
        }
        $result = $this->directory_api('sources');
        foreach ($result['sources'] ?? [] as $source) {
            if (($source['id'] ?? '') === 'company' && ($source['readonly'] ?? false) === true && ($source['write'] ?? true) === false) {
                $args['sources']['anas_company'] = ['id'=>'anas_company','name'=>$this->gettext('company'),'readonly'=>true,'groups'=>true,'autocomplete'=>true];
            }
        }
        return $args;
    }

    public function addressbook_get($args)
    {
        if (($args['id'] ?? '') === 'anas_company' && !empty($this->binding['company_directory'])) {
            $args['instance'] = new anas_company_directory($this);
        }
        return $args;
    }

    private function validate_credential($c, $previous = null)
    {
        foreach (['session_id', 'mailbox_id'] as $key) {
            if (!is_string($c[$key] ?? null) || !preg_match('/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/D', $c[$key])) {
                $this->fail(503);
            }
        }
        foreach (['access_token' => 'acc_', 'smtp_token' => 'smt_', 'refresh_token' => 'ref_'] as $key => $prefix) {
            if (!is_string($c[$key] ?? null) || !preg_match('/^' . $prefix . '[a-f0-9]{64}$/D', $c[$key])) {
                $this->fail(503);
            }
        }
        if (!preg_match('/^mbx_[a-f0-9]{32}@mailboxes\.anas\.invalid$/D', $c['username'] ?? '')
            || !preg_match('/^sub_[a-f0-9]{32}@mailboxes\.anas\.invalid$/D', $c['smtp_username'] ?? '')
            || !preg_match('/^webmail_[a-f0-9]{32}$/D', $c['roundcube_key'] ?? '')
            || !is_array($c['addresses'] ?? null) || count($c['addresses']) < 1 || count($c['addresses']) > 1000
            || !in_array($c['default_address'] ?? '', $c['addresses'], true)
            || strtotime($c['expires_at'] ?? '') <= time() || strtotime($c['refresh_expires_at'] ?? '') <= time()) {
            $this->fail(503);
        }
        foreach ($c['addresses'] as $address) {
            if (!is_string($address) || !filter_var($address, FILTER_VALIDATE_EMAIL) || strtolower($address) !== $address) {
                $this->fail(503);
            }
        }
        if ($previous !== null) {
            foreach (['session_id', 'mailbox_id', 'roundcube_key', 'username'] as $key) {
                if (($previous[$key] ?? null) !== $c[$key]) {
                    $this->fail(401);
                }
            }
        }
    }

    private function save($c)
    {
        $rc = rcmail::get_instance();
        $_SESSION['password'] = $rc->encrypt('Bearer ' . $c['access_token']);
        $c['smtp_token'] = $rc->encrypt($c['smtp_token']);
        $c['refresh_token'] = $rc->encrypt($c['refresh_token']);
        unset($c['access_token']);
        $_SESSION['anas_mail'] = $c;
    }

    public function startup($args)
    {
        $rc = rcmail::get_instance();
        $this->serialize_session();
        if ($args['action'] === 'plugin.anas_begin') {
            $form = $this->form(['mailbox_id', 'start_nonce', 'issued_at', 'signature']);
            if (!preg_match('/^[a-f0-9-]{36}$/D', $form['mailbox_id'])
                || !preg_match('/^[A-Za-z0-9_-]{43}$/D', $form['start_nonce'])
                || !preg_match('/^[0-9]{10}$/D', $form['issued_at'])
                || (int) $form['issued_at'] < time() - 60 || (int) $form['issued_at'] > time() + 5) {
                $this->fail(401);
            }
            $mac = hash_hmac('sha256', "anas-mail-webmail-start-v1\n{$form['mailbox_id']}\n{$form['start_nonce']}\n{$form['issued_at']}", hex2bin($this->binding['service_token']));
            if (!hash_equals($mac, $form['signature'])) {
                $this->fail(401);
            }
            $rc->kill_session();
            $rc->session->regenerate_id();
            $state = rtrim(strtr(base64_encode(random_bytes(32)), '+/', '-_'), '=');
            $issued = (string) time();
            $_SESSION['anas_handoff'] = ['mailbox_id' => $form['mailbox_id'], 'state' => $state, 'issued_at' => (int) $issued];
            $signature = hash_hmac('sha256', "anas-mail-webmail-handoff-v1\n{$form['mailbox_id']}\n{$state}\n{$issued}", hex2bin($this->binding['service_token']));
            $this->post_to_go('/webmail/continue', ['mailbox_id' => $form['mailbox_id'], 'browser_state' => $state, 'issued_at' => $issued, 'signature' => $signature]);
        }
        if ($args['action'] === 'plugin.anas_login') {
            $form = $this->form(['code', 'session_id', 'mailbox_id', 'browser_state']);
            $pending = $_SESSION['anas_handoff'] ?? [];
            if (($pending['mailbox_id'] ?? '') !== $form['mailbox_id'] || !isset($pending['state'])
                || !hash_equals($pending['state'], $form['browser_state']) || ($pending['issued_at'] ?? 0) < time() - 60) {
                $this->fail(401);
            }
            unset($_SESSION['anas_handoff']);
            // Persist consumption before network I/O, including unavailable Go.
            $rc->session->remove('anas_handoff');
            $this->credential = $this->api('redeem', $form);
            $this->validate_credential($this->credential);
            if ($this->credential['session_id'] !== $form['session_id'] || $this->credential['mailbox_id'] !== $form['mailbox_id']) {
                $this->fail(401);
            }
            $args['task'] = 'login';
            $args['action'] = 'login';
        } elseif (!empty($_SESSION['user_id'])) {
            $c = $_SESSION['anas_mail'] ?? null;
            if (!is_array($c) || ($_SESSION['username'] ?? '') !== $c['roundcube_key'] || $rc->user->ID != $_SESSION['user_id']) {
                $this->fail(401);
            }
            if ($args['task'] === 'logout') {
                // Core checks its own CSRF token before killing this session
                // and calling logout_after. Directory/IdP outages must not
                // prevent the local terminal withdrawal.
                $this->logoutCredential = $c;
                return $args;
            }
            $includeStatus = $args['task'] === 'mail' && $args['action'] === '';
            $active = $this->api('session', ['refresh_token' => $rc->decrypt($c['refresh_token'])], $includeStatus);
            foreach (['session_id', 'mailbox_id', 'roundcube_key', 'default_address'] as $key) {
                if (($active[$key] ?? null) !== $c[$key] || ($active['active'] ?? false) !== true) {
                    $this->fail(401);
                }
            }
            if ($includeStatus) {
                $outbound = $active['outbound_status'] ?? 'unknown';
                $this->mailStatus = in_array($outbound, ['not-configured', 'unknown', 'unverified'], true) ? $outbound : 'unknown';
            }
            if (strtotime($c['expires_at']) < time() + 30) {
                $next = $this->api('refresh', ['refresh_token' => $rc->decrypt($c['refresh_token'])]);
                $this->validate_credential($next, $c);
                $this->save($next);
                $this->sync_identities();
            }
            if ($args['task'] === 'mail' && $args['action'] === 'send') {
                $this->set_submission_confirmation($rc);
            }
        }
        return $args;
    }

    private function set_submission_confirmation($rc)
    {
        // A successful Roundcube send means SMTP submission succeeded; it
        // does not establish the recipient server's delivery outcome.
        $language = $_SESSION['language'] ?? null;
        $texts = $rc->read_localization(__DIR__ . '/localization/', $language);
        if (isset($texts['submissionaccepted']) && is_string($texts['submissionaccepted']) && $texts['submissionaccepted'] !== '') {
            $rc->load_language($language, [], ['messagesent' => $texts['submissionaccepted']]);
        }
    }

    public function render_page($args)
    {
        if (($args['template'] ?? '') !== 'mail' || $this->mailStatus === null || empty($_SESSION['anas_mail']) || empty($_SESSION['user_id'])) {
            return $args;
        }
        $key = $this->mailStatus === 'not-configured' ? 'outboundmissing' : ($this->mailStatus === 'unknown' ? 'outboundunknown' : 'publicunverified');
        $message = htmlspecialchars($this->gettext($key), ENT_QUOTES, 'UTF-8');
        $more = htmlspecialchars($this->gettext('management'), ENT_QUOTES, 'UTF-8');
        $url = htmlspecialchars($this->binding['go_origin'] . '/', ENT_QUOTES, 'UTF-8');
        $banner = '<div class="anas-mail-status" role="status">' . $message . ' <a href="' . $url . '">' . $more . '</a></div>';
        $args['content'] = preg_replace_callback('/(<div id="layout-list" class="listbox selected">)/', function ($match) use ($banner) { return $match[1] . $banner; }, $args['content'], 1);
        return $args;
    }

    public function authenticate($args)
    {
        if (!$this->credential) {
            $args['valid'] = false;
            return $args;
        }
        $args['user'] = $this->credential['roundcube_key'];
        $args['pass'] = 'Bearer ' . $this->credential['access_token'];
        $args['host'] = 'ssl://' . $this->binding['dms_host'] . ':993';
        $args['valid'] = true;
        $args['cookiecheck'] = false;
        $this->save($this->credential);
        return $args;
    }

    public function storage_connect($args)
    {
        $c = $_SESSION['anas_mail'] ?? null;
        if (!$c) {
            $this->fail(401);
        }
        $args['host'] = $this->binding['dms_host'];
        $args['port'] = 993;
        $args['ssl_mode'] = 'ssl';
        $args['user'] = $c['username'];
        $args['pass'] = rcmail::get_instance()->decrypt($_SESSION['password']);
        $args['auth_type'] = 'XOAUTH2';
        $args['socket_options'] = $this->tls_options();
        return $args;
    }

    private function tls_options()
    {
        return ['ssl' => ['verify_peer' => true, 'verify_peer_name' => true, 'allow_self_signed' => false, 'cafile' => $this->binding['ca_file'], 'peer_name' => $this->binding['dms_host']]];
    }

    public function smtp_connect($args)
    {
        $c = $_SESSION['anas_mail'] ?? null;
        if (!$c) {
            $this->fail(401);
        }
        $args['smtp_host'] = 'tls://' . $this->binding['dms_host'] . ':587';
        $args['smtp_auth_type'] = 'XOAUTH2';
        $args['smtp_user'] = $c['smtp_username'];
        $args['smtp_pass'] = 'Bearer ' . rcmail::get_instance()->decrypt($c['smtp_token']);
        $args['smtp_conn_options'] = $this->tls_options();
        return $args;
    }

    public function user_create($args)
    {
        if (!$this->credential) {
            $this->fail(401);
        }
        $args['email'] = $this->credential['default_address'];
        $args['name'] = $this->credential['default_address'];
        return $args;
    }

    private function sync_identities()
    {
        $rc = rcmail::get_instance();
        $c = $_SESSION['anas_mail'];
        $known = array_column($rc->user->list_identities(), 'email');
        foreach ($c['addresses'] as $address) {
            if (!in_array($address, $known, true) && !$rc->user->insert_identity(['email' => $address, 'name' => $address])) {
                $this->fail(503);
            }
        }
        foreach ($rc->user->list_identities() as $identity) {
            if (!in_array($identity['email'], $c['addresses'], true)) {
                $rc->user->delete_identity($identity['identity_id']);
            } elseif ($identity['email'] === $c['default_address']) {
                $rc->user->set_default($identity['identity_id']);
            }
        }
    }

    public function login_after($args)
    {
        $this->save($this->credential);
        $this->sync_identities();
        return ['_task' => 'mail'];
    }

    public function logout_after($args)
    {
        if (!$this->logoutCredential) {
            $this->fail(401);
        }
        $result = $this->api('logout', ['refresh_token' => rcmail::get_instance()->decrypt($this->logoutCredential['refresh_token'])]);
        if (($result['post_to'] ?? '') !== $this->binding['go_origin'] . '/webmail/logout-continue'
            || ($result['fields']['session_id'] ?? '') !== $this->logoutCredential['session_id']) {
            $this->fail(503);
        }
        $this->post_to_go('/webmail/logout-continue', $result['fields']);
    }

    public function unauthenticated($args)
    {
        header('Location: ' . $this->binding['go_origin'] . '/', true, 303);
        exit;
    }

    public function deny_identity_change($args)
    {
        $args['abort'] = true;
        $args['result'] = false;
        return $args;
    }

    public function identity_update($args)
    {
        $current = rcmail::get_instance()->user->get_identity($args['id']);
        if (!$current || !in_array($current['email'], $_SESSION['anas_mail']['addresses'] ?? [], true)) {
            return $this->deny_identity_change($args);
        }
        $args['record']['email'] = $current['email'];
        $args['record']['standard'] = $current['email'] === $_SESSION['anas_mail']['default_address'] ? 1 : 0;
        return $args;
    }

    public function display_username($args)
    {
        if (!empty($_SESSION['anas_mail']['default_address'])) {
            $args['content'] = '<a href="' . htmlspecialchars($this->binding['go_origin'] . '/', ENT_QUOTES, 'UTF-8') . '" title="'
                . htmlspecialchars($this->gettext('management'), ENT_QUOTES, 'UTF-8') . '">'
                . htmlspecialchars($_SESSION['anas_mail']['default_address'], ENT_QUOTES, 'UTF-8') . '</a>';
        }
        return $args;
    }
}
