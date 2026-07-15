
module Anas
  class PostgresRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = ['POSTGRES_PASSWORD', 'POSTGRES_USERNAME']
      @optional_envs = [
        'POSTGRES_ADMINER_ENABLED',
      ]
      @default_envs = {
        'POSTGRES_USERNAME' => 'postgres', 'POSTGRES_ADMINER_ENABLED' => false,
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['POSTGRES_NETWORK_NAME'] = "#{new_envs['NETWORK_PREFIX']}postgres" unless envs.has_key?('POSTGRES_NETWORK_NAME')
      new_envs['POSTGRES_PASSWORD'] = envs['DEFAULT_SERVICE_ROOT_PASSWORD'] unless envs.has_key?('POSTGRES_PASSWORD')
      new_envs['POSTGRES_USER'] = new_envs['POSTGRES_USERNAME']
      new_envs['POSTGRES_HOST'] = "#{new_envs['CONTAINER_PREFIX']}postgres"
      new_envs['POSTGRES_PORT'] = '5432'
      new_envs['POSTGRES_HOST_PORT'] = "#{new_envs['POSTGRES_HOST']}:#{new_envs['POSTGRES_PORT']}"

      new_envs['POSTGRES_ADMINER_DOMAIN_PREFIX'] = 'postgres_adminer'
      new_envs['POSTGRES_ADMINER_DOMAIN'] = "#{envs['POSTGRES_ADMINER_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      return new_envs
    end
    
    def module_envs(envs)
      new_envs = envs

      # new_envs['POSTGRES_INITDB_ARGS'] = '--auth-host=scram-sha-256'
      new_envs['POSTGRES_HOST_AUTH_METHOD'] = 'trust'
      new_envs['ADMINER_DESIGN'] = 'nette'

      new_envs
    end

    def run_after_mods(envs)
      return ['traefik']
    end

    def docker_services_list
      list = super
      if @envs['POSTGRES_ADMINER_ENABLED'] == 'true'
        return list
      else
        return list.minus 'postgres_adminer'
      end
    end

    def self.dependent_mods(base_envs)
      if base_envs['POSTGRES_ADMINER_ENABLED'] == 'true'
        return ['traefik']
      else
        return ['core']
      end
    end
    
  end
end