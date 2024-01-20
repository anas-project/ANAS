
module Anas
  class TraefikRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @optional_envs = [
        'TRAEFIK_BASE_PORT', 'TRAEFIK_DOMAIN_PREFIX'
      ]
      @default_envs = {'TRAEFIK_BASE_PORT' => 9000, 
        'TRAEFIK_DOMAIN_PREFIX' => 'traefik'
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['TRAEFIK_HOSTNAME'] = "#{new_envs['CONTAINER_PREFIX']}traefik"
      new_envs['TRAEFIK_DOMAIN'] = "#{envs['TRAEFIK_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['TRAEFIK_DOMAIN_PORT'] = "#{envs['TRAEFIK_DOMAIN']}:#{envs['TRAEFIK_BASE_PORT']}"
      new_envs['TRAEFIK_DOMAIN_FULL'] = "https://#{envs['TRAEFIK_DOMAIN_PORT']}"
      return new_envs
    end

    def self.dependent_mods(base_envs)
      return ['lego']
    end


  end
end