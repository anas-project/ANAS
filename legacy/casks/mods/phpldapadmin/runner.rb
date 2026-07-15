
module Anas
  class PhpldapadminRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = []
      @optional_envs = ['PHPLDAPADMIN_DOMAIN_PREFIX',]
      @default_envs = {
        'PHPLDAPADMIN_DOMAIN_PREFIX' => 'phpldapadmin'
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['PHPLDAPADMIN_DOMAIN'] = "#{envs['PHPLDAPADMIN_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      return new_envs
    end

    def run_after_mods(envs)
      return ['samba_dc']
    end

    def self.dependent_mods(base_envs)
      return ['traefik']
    end

  end
end