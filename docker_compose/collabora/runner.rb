
module Anas
  class CollaboraRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = []
      @optional_envs = [
        'COLLABORA_DOMAIN_PREFIX', 'COLLABORA_LOG_LEVEL', 'COLLABORA_DOMAIN_INTERFACE',
        'COLLABORA_AUTO_SAVE',
      ]
      @default_envs = {
        'COLLABORA_DOMAIN_PREFIX' => 'collabora', 'COLLABORA_LOG_LEVEL' => 'warning',
        'COLLABORA_INTERFACE' => 'default', 'COLLABORA_AUTO_SAVE' => '60',
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['COLLABORA_HOSTNAME'] = "#{new_envs['CONTAINER_PREFIX']}collabora"
      new_envs['COLLABORA_DOMAIN'] = "#{envs['COLLABORA_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['COLLABORA_DOMAIN_PORT'] = "#{new_envs['COLLABORA_DOMAIN']}:#{new_envs['TRAEFIK_BASE_PORT']}"
      new_envs['COLLABORA_DOMAIN_FULL'] = "https://#{new_envs['COLLABORA_DOMAIN_PORT']}"
      return new_envs
    end

    def self.dependent_mods(base_envs)
      return ['traefik']
    end

    def module_envs(envs)
      new_envs = envs
      new_envs['TIMEZONE'] = envs['TZ']
      new_envs['CONTAINER_NAME'] = "#{envs['CONTAINER_PREFIX']}collabora"
      new_envs['ADMIN_USER'] = envs['SAMBA_DC_ADMIN_NAME']
      new_envs['ADMIN_PASS'] = envs['DEFAULT_ROOT_PASSWORD']
      new_envs['ALLOWED_HOSTS'] = "#{envs['NEXTCLOUD_DOMAIN_FULL']}"
      new_envs['INTERFACE'] = envs['COLLABORA_INTERFACE']
      new_envs['LOG_TYPE'] = 'CONSOLE'
      new_envs['LOG_LEVEL'] = envs['COLLABORA_LOG_LEVEL']
      new_envs['ENABLE_TLS'] = 'FALSE'
      new_envs['ENABLE_TLS_CERT_GENERATE'] = 'FALSE'
      new_envs['ENABLE_TLS_REVERSE_PROXY'] = 'TRUE'
      new_envs['AUTO_SAVE'] = envs['COLLABORA_AUTO_SAVE']
      new_envs['HOSTNAME'] = envs['COLLABORA_DOMAIN_PORT']
      new_envs['FRAME_ANCESTORS'] = 'https://*'
      new_envs['ENABLE_CLEANUP'] = 'true'
      # new_envs['EXTRA_OPTIONS'] = "--port #{envs['TRAEFIK_BASE_PORT']}"
      return new_envs
    end

  end
end