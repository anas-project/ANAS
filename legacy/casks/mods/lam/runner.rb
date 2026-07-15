
module Anas
  class LamRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = ['LAM_ADMIN_PASSWORD']
      @optional_envs = [
        'LAM_DOMAIN_PREFIX', 'LAM_ADMIN_PASSWORD', 'LAM_LANGUAGE'
      ]
      @default_envs = {
        'LAM_DOMAIN_PREFIX' => 'lam'
      }
    end

    def self.dependent_mods(base_envs)
      return ['traefik']
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['LAM_LANGUAGE'] = envs['DEFAULT_LANGUAGE'] unless envs.has_key?('LAM_LANGUAGE')
      new_envs['LAM_DOMAIN'] = "#{envs['LAM_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['LAM_ADMIN_PASSWORD'] = envs['SAMBA_DC_ADMIN_PASSWORD'] unless envs.has_key?('LAM_ADMIN_PASSWORD')
      return new_envs
    end

    def run_after_mods(envs)
      return ['samba_dc']
    end
  end
end