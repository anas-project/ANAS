
module Anas
  class MeshcentralRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      # @required_envs = ['MESHCENTRAL_ROOT_PASSWORD']
      @optional_envs = [
        'MESHCENTRAL_DOMAIN_PREFIX', 'MESHCENTRAL_TITLE', 'MESHCENTRAL_SUBTITLE',
      ]
      @default_envs = {
        'MESHCENTRAL_DOMAIN_PREFIX' => 'meshcentral', 'MESHCENTRAL_MPS_PORT' => '4433'
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['MESHCENTRAL_DOMAIN'] = "#{envs['MESHCENTRAL_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['MESHCENTRAL_TITLE'] = envs['SERVER_NAME'] unless envs.has_key?('MESHCENTRAL_TITLE')
      new_envs['MESHCENTRAL_SUBTITLE'] = ' ' unless envs.has_key?('MESHCENTRAL_SUBTITLE')
      unless envs['MESHCENTRAL_USER_FILTER']
        if envs['SAMBA_DC_APP_FILTER'] == 'true'
          new_envs['MESHCENTRAL_USER_FILTER'] = "(&#{envs['SAMBA_DC_USER_CLASS_FILTER']}(memberOf=CN=APP_meshcentral,#{envs['SAMBA_DC_BASE_APP_DN']}))"
        else
          new_envs['MESHCENTRAL_USER_FILTER'] = envs['SAMBA_DC_USER_CLASS_FILTER']
        end
      end
      unless envs['MESHCENTRAL_USER_LOGIN_FILTER']
        attrs = envs['SAMBA_DC_USER_LOGIN_ATTRS'].split(',')
        uid_filter = "(|#{(attrs.map { |attr| "(#{attr}={{username}})"}).join})"
        new_envs['MESHCENTRAL_USER_LOGIN_FILTER'] = "(&#{new_envs['MESHCENTRAL_USER_FILTER']}#{envs['SAMBA_DC_USER_ENABLED_FILTER']}#{uid_filter})"
      end

      return new_envs
    end

    def self.dependent_mods(base_envs)
      return ['traefik', 'mariadb']
    end

    def use_ldap?
      return true
    end

    def run_after_mods(envs)
      return ['samba_dc']
    end
  end
end