package dms

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type RuntimeConfig struct{ GoURL, DovecotToken, SenderToken, CAFile string }

var serviceKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

func RuntimeFiles(cfg RuntimeConfig) ([]byte, []byte, error) {
	u, err := url.Parse(cfg.GoURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Port() != "8443" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(cfg.GoURL, "\x00\r\n") || !serviceKey.MatchString(cfg.DovecotToken) || !serviceKey.MatchString(cfg.SenderToken) || cfg.DovecotToken == cfg.SenderToken || !filepath.IsAbs(cfg.CAFile) || strings.ContainsAny(cfg.CAFile, "\x00\r\n\"\\ ") {
		return nil, nil, errors.New("DMS runtime binding invalid")
	}
	base := u.String()
	u.Path = "/internal/protocol/introspect"
	u.User = url.UserPassword("dovecot", cfg.DovecotToken)
	dovecot := fmt.Sprintf(`ssl_client_ca_file = %s
auth_mechanisms = oauthbearer xoauth2
auth_cache_size = 0
oauth2 {
  introspection_url = %s
  introspection_mode = post
  username_attribute = username
  active_attribute = active
  active_value = true
  force_introspection = yes
  send_auth_headers = yes
}


oauth2_fields {
  user_anas_sid = %%{oauth2:anas_sid}
  userdb_user_anas_sid = %%{oauth2:anas_sid}
  user_anas_context = %%{oauth2:anas_context}
  userdb_user_anas_context = %%{oauth2:anas_context}
}
`, cfg.CAFile, u.String())
	endpoint, _ := json.Marshal(base + "/internal/protocol/sender")
	auth, _ := json.Marshal("Basic " + base64.StdEncoding.EncodeToString([]byte("sender:"+cfg.SenderToken)))
	lua := fmt.Sprintf(`-- This adapter decides no ownership and never overrides spam acceptance.
local http = require 'rspamd_http'
local ucl = require 'ucl'
local util = require 'rspamd_util'
rspamd_config:register_symbol({
  name = 'ANAS_MAIL_SENDER', type = 'prefilter', priority = 20,
  callback = function(task)
    local login = task:get_user()
    if not login or login == '' then return end
    local queue = task:get_queue_id()
    if not queue or queue == '' then
      task:set_pre_result('soft reject', 'Mail authorization unavailable', 'anas_mail')
      return
    end
    local function addresses(kind)
      local result = {}
      for _, item in ipairs(task:get_from(kind) or {}) do table.insert(result, item.addr or '') end
      return result
    end
    local request_id = util.random_hex(32)
    if not request_id or not request_id:match('^[a-f0-9]+$') or #request_id ~= 32 then
      task:set_pre_result('soft reject', 'Mail authorization unavailable', 'anas_mail')
      return
    end
    local queued = http.request({task = task, url = %s,
      method = 'POST', timeout = 5, max_size = 4096, no_ssl_verify = false,
      mime_type = 'application/json', headers = {Authorization = %s},
      body = ucl.to_format({login=login, request_id=request_id, queue_id=queue,
        envelope=addresses('smtp'), from=addresses('mime'),
        from_count=#(task:get_header_full('From') or {})}, 'json-compact'),
      callback = function(err, code)
        if err or (code ~= 204 and code ~= 403) then
          task:set_pre_result('soft reject', 'Mail authorization unavailable', 'anas_mail')
        elseif code == 403 then
          task:set_pre_result('reject', 'Sender address not authorized for this mailbox', 'anas_mail')
        end
      end})
    if not queued then task:set_pre_result('soft reject', 'Mail authorization unavailable', 'anas_mail') end
  end
})
`, endpoint, auth)
	return []byte(dovecot), []byte(lua), nil
}

// PrepareRuntime is invoked only by the fixed container startup command, before
// DMS loads configuration. HTTP cannot call it or select any output path.
func PrepareRuntime(dovecot, lua, trust []byte, rspamdGID int) error {
	if rspamdGID < 1 {
		return ErrConflict
	}
	if err := atomicFile("/tmp/docker-mailserver/dovecot.cf", dovecot, 0600); err != nil {
		return err
	}
	const luaPath = "/etc/rspamd/rspamd.local.lua"
	if err := atomicFile(luaPath, lua, 0640); err != nil {
		return err
	}
	if err := os.Chown(luaPath, 0, rspamdGID); err != nil {
		return err
	}
	if err := atomicFile("/usr/local/share/ca-certificates/anas-mail-trust.crt", trust, 0644); err != nil {
		return err
	}
	cmd := exec.Command("/usr/sbin/update-ca-certificates")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if cmd.Run() != nil {
		return ErrUnavailable
	}
	return nil
}
