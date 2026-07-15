
module Anas
  class MariadbRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = ['MARIADB_ROOT_PASSWORD']
      @optional_envs = [
        'MARIADB_ADMINER_ENABLED',
      ]
      @default_envs = {
        'MARIADB_ADMINER_ENABLED' => false,
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['MARIADB_NETWORK_NAME'] = "#{new_envs['NETWORK_PREFIX']}mariadb" unless envs.has_key?('MARIADB_NETWORK_NAME')
      new_envs['MARIADB_ROOT_PASSWORD'] = envs['DEFAULT_SERVICE_ROOT_PASSWORD'] unless envs.has_key?('MARIADB_ROOT_PASSWORD')
      new_envs['MARIADB_PASSWORD'] = new_envs['MARIADB_ROOT_PASSWORD']
      new_envs['MARIADB_USERNAME'] = 'root'
      new_envs['MARIADB_HOST'] = "#{new_envs['CONTAINER_PREFIX']}mariadb"
      new_envs['MARIADB_PORT'] = '3306'
      new_envs['MARIADB_HOST_PORT'] = "#{new_envs['MARIADB_HOST']}:3306"

      new_envs['MARIADB_ADMINER_DOMAIN_PREFIX'] = 'mariadb_adminer'
      new_envs['MARIADB_ADMINER_DOMAIN'] = "#{envs['MARIADB_ADMINER_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      return new_envs
    end

    def module_envs(envs)
      new_envs = envs

      new_envs['ADMINER_DESIGN'] = 'nette'
      new_envs['MYSQL_ROOT_PASSWORD'] = envs['MARIADB_ROOT_PASSWORD']

      return new_envs
    end

    def run_after_mods(envs)
      return ['traefik']
    end

    def docker_services_list
      list = super
      if @envs['MARIADB_ADMINER_ENABLED'] == 'true'
        return list
      else
        return list.minus 'mariadb_adminer'
      end
    end

    def self.dependent_mods(base_envs)
      if base_envs['MARIADB_ADMINER_ENABLED'] == 'true'
        return ['traefik']
      else
        return ['core']
      end
    end

  end
end