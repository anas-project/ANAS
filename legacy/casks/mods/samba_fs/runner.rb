require 'resolv'

module Anas
  class SambaFsRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = ['SAMBA_DC_REALM', 'SAMBA_DC_DC_DOMAIN', 
        'SAMBA_DC_DOMAIN', 'SAMBA_DC_WORKGROUP',
        'SAMBA_DC_ADMINISTRATOR_NAME', 'SAMBA_DC_ADMINISTRATOR_PASSWORD',
        # SAMBA_FS
        'SHARE_DIR_NAME',
      ]
      @optional_envs = [ 'SAMBA_FS_INTERFACES',
        'SAMBA_FS_HOSTNAME', 'SAMBA_FS_USE_DEFAULT_DOMAIN',
        'SAMBA_FS_LOG_LEVEL', 'SAMBA_FS_WSDD_LOG_LEVEL'
      ]
      @default_envs = { 'SAMBA_FS_LOG_LEVEL' => 1, 'SAMBA_FS_WSDD_LOG_LEVEL' => 0,
        'SAMBA_FS_HOSTNAME' => 'SambaFS'
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['SAMBA_FS_USE_DEFAULT_DOMAIN'] = envs['USE_DEFAULT_DOMAIN'] unless envs.has_key?('SAMBA_FS_USE_DEFAULT_DOMAIN')
      new_envs['SAMBA_FS_NETBIOS_NAME'] = envs['SAMBA_FS_HOSTNAME'].upcase unless envs.has_key?('SAMBA_FS_NETBIOS_NAME')
      return new_envs
    end

    def run_after_mods(envs)
      return ['samba_dc']
    end

    def use_host_lan?
      return 'required'
    end
  end
end