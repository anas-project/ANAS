
module Anas
  class EturnalRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      # @required_envs = ['MARIADB_ROOT_PASSWORD']
      @optional_envs = [
        'TURN_PORT', 'TURN_DOMAIN_PREFIX'
      ]
      @default_envs = {
        'TURN_PORT' => 3478, 'TURN_DOMAIN_PREFIX' => 'turn',
      }
    end

    def cal_envs(envs)
      new_envs = envs
      if envs.has_key?('TURN_DOMAIN')
        raise ModConflictError.new("TURN_DOMAIN already exists in envs. Mod conflict.")
      end
      new_envs['TURN_HOSTNAME'] = "#{new_envs['CONTAINER_PREFIX']}eturnal"
      new_envs['TURN_SECRET'] = String.random_password(32)
      new_envs['TURN_DOMAIN'] = "#{envs['TURN_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['TURN_DOMAIN_PORT'] = "#{envs['TURN_DOMAIN']}:#{envs['TURN_PORT']}"
      return new_envs
    end

    def module_envs(envs)
      new_envs = envs
      new_envs['TURN_RELAY_MIN_PORT'] = 50000
      new_envs['TURN_RELAY_MAX_PORT'] = 51000
      return new_envs
    end

    def services_name
      return ['turn']
    end

  end
end