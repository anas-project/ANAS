
module Anas
  class NetbirdRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = []
      @optional_envs = [
        'NETBIRD_DOMAIN_PREFIX',
      ]
      @default_envs = {
        'NETBIRD_DOMAIN_PREFIX' => 'netbird',
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['NETBIRD_DOMAIN'] = "#{envs['NETBIRD_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['NETBIRD_DOMAIN_PORT'] = "#{envs['NETBIRD_DOMAIN']}:#{envs['TRAEFIK_BASE_PORT']}"
      new_envs['NETBIRD_DOMAIN_FULL'] = "https://#{envs['NETBIRD_DOMAIN_PORT']}"

      if envs['SAMBA_DC_APP_FILTER'] == 'true'
        # allow_groups = "APP_nextcloud, #{envs['SAMBA_DC_APP_ALL_NAME']}"
        allow_groups = "APP_netbird"
      else
        allow_groups = ""
      end
      new_envs['OIDC_RP_APPS'] = '' unless envs.has_key?('OIDC_RP_APPS')
      openid_apps = new_envs['OIDC_RP_APPS'].split(',')
      openid_apps.push 'netbird' unless openid_apps.include? 'netbird'
      new_envs['OIDC_RP_APPS'] = openid_apps.join(',')
      # var name, attr name, mandatory
      new_envs['OIDC_RP__NETBIRD__ATTR01'] = "cn,cn,1"
      new_envs['OIDC_RP__NETBIRD__ATTR02'] = "sAMAccountName,sAMAccountName,1"
      new_envs['OIDC_RP__NETBIRD__ATTR03'] = "email,email,1"
      # new_envs['OIDC_RP__NETBIRD__ATTR03'] = "sAMAccountName,sAMAccountName,1"
      new_envs['OIDC_RP__NETBIRD__CLIENT_ID'] = 'netbird'
      new_envs['OIDC_RP__NETBIRD__CLIENT_SECRET'] = String.random(12)
      new_envs['OIDC_RP__NETBIRD__REDIRECT_URI'] = "#{envs['NETBIRD_DOMAIN_FULL']}/auth, #{envs['NETBIRD_DOMAIN_FULL']}/silent-auth"
      new_envs['OIDC_RP__NETBIRD__LOGOUT_REDIRECT_URI'] = envs['NETBIRD_DOMAIN_FULL']
      new_envs['OIDC_RP__NETBIRD__ALLOW_GROUPS'] = allow_groups
      new_envs['OIDC_RP__NETBIRD__DOMAIN'] = new_envs['NETBIRD_DOMAIN']

      new_envs['APPS_LIST'] = '' unless envs.has_key?('APPS_LIST')
      apps = new_envs['APPS_LIST'].split(',')
      apps.push 'netbird' unless apps.include? 'netbird'
      new_envs['APPS_LIST'] = apps.join(',')
      new_envs['APPS_LIST__NETBIRD__NAME'] = 'Netbird' unless envs.has_key?('APPS_LIST__NETBIRD__NAME')
      new_envs['APPS_LIST__NETBIRD__DESC'] = 'Connect and Secure Your IT Infrastructure in Minutes' unless envs.has_key?('APPS_LIST__NETBIRD__DESC')
      if envs.has_key?('APPS_LIST__NETBIRD__LOGO_PATH')
        # TODO: path
        # new_envs['APPS_LIST__NETBIRD__LOGO_PATH'] = 
      else
        new_envs['APPS_LIST__NETBIRD__LOGO_PATH'] = File.join(@working_path, '/assets/netbird.png')
      end
      new_envs['APPS_LIST__NETBIRD__URI'] = new_envs['NETBIRD_DOMAIN_FULL']
      new_envs['APPS_LIST__NETBIRD__ALLOW_GROUPS'] = allow_groups

      return new_envs
    end
    
    def module_envs(envs)
      new_envs = envs

      new_envs['AUTH_AUDIENCE'] = 'netbird'

      new_envs['NETBIRD_DASHBOARD_ENDPOINT'] = envs['NETBIRD_DOMAIN_FULL']
      new_envs['NETBIRD_MGMT_API_ENDPOINT'] = envs['NETBIRD_DOMAIN_FULL']
      new_envs['NETBIRD_MGMT_GRPC_API_ENDPOINT'] = envs['NETBIRD_DOMAIN_FULL']
      new_envs['NETBIRD_MGMT_API_PORT'] = envs['TRAEFIK_BASE_PORT']
      new_envs['AUTH_CLIENT_ID'] = envs['OIDC_RP__NETBIRD__CLIENT_ID']
      new_envs['AUTH_CLIENT_SECRET'] = envs['OIDC_RP__NETBIRD__CLIENT_SECRET']

      new_envs['NETBIRD_SIGNAL_ENDPOINT'] = envs['NETBIRD_DOMAIN_FULL']
      new_envs['NETBIRD_SIGNAL_PORT'] = envs['TRAEFIK_BASE_PORT']

      new_envs['AUTH_REDIRECT_URI'] = '/auth'
      new_envs['AUTH_SILENT_REDIRECT_URI'] = '/silent-auth'

      new_envs['NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT'] = envs['LLNG_OIDC_CONFIGURATION_ENDPOINT']
      new_envs['AUTH_SUPPORTED_SCOPES'] = "openid profile email"
      new_envs['AUTH_DEVICE_AUTH_PROVIDER'] = false
      new_envs['USE_AUTH0'] = false
      # new_envs['AUTH_AUTHORITY'] = 

      new_envs
    end

    def docker_services_list
      list = super
      if @envs['NETBIRD_ADMINER_ENABLED'] == 'true'
        return list
      else
        return list.minus 'NETBIRD_adminer'
      end
    end

    def self.dependent_mods(base_envs)
      if base_envs['NETBIRD_ADMINER_ENABLED'] == 'true'
        return ['traefik']
      else
        return ['core']
      end
    end
    
  end
end