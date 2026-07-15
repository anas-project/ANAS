require 'openssl'

module Anas
  class KeycloakRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = []
      @optional_envs = [
        'KEYCLOAK_DOMAIN_PREFIX', 'KEYCLOAK_MANAGER_DOMAIN_PREFIX', 'KEYCLOAK_TEST_DOMAIN_PREFIX',
        'KEYCLOAK_LOG_LEVEL',
        'KEYCLOAK_DB_TYPE', 'KEYCLOAK_ENABLE_TEST',
      ]
      @default_envs = {
        'KEYCLOAK_DOMAIN_PREFIX' => 'auth', 'KEYCLOAK_MANAGER_DOMAIN_PREFIX' => 'auth-manager',
        'KEYCLOAK_TEST_DOMAIN_PREFIX' => 'auth-test',
        'KEYCLOAK_LOG_LEVEL' => 'warn', 'KEYCLOAK_DB_NAME' => 'lemonldap-ng',
        'KEYCLOAK_ENABLE_TEST' => true,
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['KEYCLOAK_PASSWORD'] = envs['DEFAULT_SERVICE_ROOT_PASSWORD'] unless envs.has_key?('KEYCLOAK_PASSWORD')
      new_envs['KEYCLOAK_DOMAIN'] = "#{envs['KEYCLOAK_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['KEYCLOAK_DOMAIN_PORT'] = "#{envs['KEYCLOAK_DOMAIN']}:#{envs['TRAEFIK_BASE_PORT']}"
      new_envs['KEYCLOAK_DOMAIN_FULL'] = "https://#{envs['KEYCLOAK_DOMAIN_PORT']}"

      new_envs['KEYCLOAK_TEST_DOMAIN'] = "#{envs['KEYCLOAK_TEST_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['KEYCLOAK_TEST_DOMAIN_PORT'] = "#{envs['KEYCLOAK_TEST_DOMAIN']}:#{envs['TRAEFIK_BASE_PORT']}"
      new_envs['KEYCLOAK_TEST_DOMAIN_FULL'] = "https://#{envs['KEYCLOAK_TEST_DOMAIN_PORT']}"

      new_envs['KEYCLOAK_MANAGER_DOMAIN'] = "#{envs['KEYCLOAK_MANAGER_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['KEYCLOAK_MANAGER_DOMAIN_PORT'] = "#{envs['KEYCLOAK_MANAGER_DOMAIN']}:#{envs['TRAEFIK_BASE_PORT']}"
      new_envs['KEYCLOAK_MANAGER_DOMAIN_FULL'] = "https://#{envs['KEYCLOAK_MANAGER_DOMAIN_PORT']}"
      new_envs['KEYCLOAK_HOST'] = 'llng'
      new_envs['KEYCLOAK_HOST_PORT'] = "#{new_envs['KEYCLOAK_HOST']}:#{new_envs['KEYCLOAK_PORT']}"

      new_envs['KEYCLOAK_HOST'] = 'llng'
      new_envs['KEYCLOAK_HANDLER_SOCKET_PORT'] = '9000'

      unless envs.has_key?('KEYCLOAK_DB_TYPE')
        if envs.has_key?('POSTGRES_HOST')
          new_envs['KEYCLOAK_DB_TYPE'] = 'postgres'
        elsif envs.has_key?('MARIADB_HOST')
          new_envs['KEYCLOAK_DB_TYPE'] = 'mariadb'
        else
          raise EnvError.new("No database for lemonldap-ng.")
        end
      end

      if new_envs['KEYCLOAK_DB_TYPE'] == 'mariadb'
        new_envs['KEYCLOAK_NETWORK_DB'] = new_envs['MARIADB_NETWORK_NAME']
      elsif new_envs['KEYCLOAK_DB_TYPE'] == 'postgres'
        new_envs['KEYCLOAK_NETWORK_DB'] = new_envs['POSTGRES_NETWORK_NAME']
      end

      new_envs['KEYCLOAK_LDAP_AUTH_FILTER'] = "(&#{new_envs['SAMBA_DC_USER_CLASS_FILTER']}#{envs['SAMBA_DC_USER_ENABLED_FILTER']}(#{envs['SAMBA_DC_USER_NAME']}=$user))"
      new_envs['KEYCLOAK_LDAP_MAIL_FILTER'] = "(&#{new_envs['SAMBA_DC_USER_CLASS_FILTER']}#{envs['SAMBA_DC_USER_ENABLED_FILTER']}(#{envs['SAMBA_DC_USER_EMAIL']}=$mail))"
      
      # SAML & OIDC service Signature
      rsa_key = OpenSSL::PKey::RSA.new(2048)
      cert = OpenSSL::X509::Certificate.new
      cert.version = 2
      cert.subject = OpenSSL::X509::Name.parse("/CN=#{new_envs['KEYCLOAK_DOMAIN']}")
      cert.issuer = cert.subject
      cert.public_key = rsa_key.public_key
      cert.not_before = Time.now
      cert.not_after = cert.not_before + 3650 * 24 * 60 * 60
      cert.sign(rsa_key, OpenSSL::Digest::SHA256.new)
      new_envs['KEYCLOAK_SAML_SERVICE_PRIVATE_KEY'] = rsa_key.to_pem.inspect
      new_envs['KEYCLOAK_SAML_SERVICE_PUBLIC_KEY'] = cert.to_pem.inspect
      new_envs['KEYCLOAK_OIDC_SERVICE_PRIVATE_KEY'] = rsa_key.to_pem.inspect
      new_envs['KEYCLOAK_OIDC_SERVICE_PUBLIC_KEY'] = cert.to_pem.inspect
      new_envs['KEYCLOAK_OIDC_SERVICE_KEY_ID'] = String.random(12)

      # SAML URI
      new_envs['KEYCLOAK_SAML_IDP_ENTITY_ID'] = "#{new_envs['KEYCLOAK_DOMAIN_FULL']}/saml/metadata"
      new_envs['KEYCLOAK_SAML_IDP_SSO'] = "#{new_envs['KEYCLOAK_DOMAIN_FULL']}/saml/singleSignOn"
      new_envs['KEYCLOAK_SAML_IDP_SLO'] = "#{new_envs['KEYCLOAK_DOMAIN_FULL']}/saml/singleLogout"
      new_envs['KEYCLOAK_SAML_IDP_SLO_RESPONSE'] = "#{new_envs['KEYCLOAK_DOMAIN_FULL']}/saml/singleLogoutReturn"

      new_envs['KEYCLOAK_OIDC_CONFIGURATION_ENDPOINT'] = "#{new_envs['KEYCLOAK_DOMAIN_FULL']}/.well-known/openid-configuration"

      return new_envs
    end
    
    def module_envs(envs)
      new_envs = envs
      # not use privilege user
      if envs['KEYCLOAK_DB_TYPE'] == 'postgres'
        new_envs['DB_HOST'] = envs['POSTGRES_HOST']
        new_envs['DB_POST'] = envs['POSTGRES_PORT']
        new_envs['DB_USER'] = envs['POSTGRES_USERNAME']
        new_envs['DB_PASSWORD'] = envs['POSTGRES_PASSWORD']
      elsif envs['KEYCLOAK_DB_TYPE'] == 'mariadb'
        new_envs['DB_HOST'] = envs['MARIADB_HOST']
        new_envs['DB_POST'] = envs['MARIADB_PORT']
        new_envs['DB_USER'] = envs['MARIADB_USERNAME']
        new_envs['DB_PASSWORD'] = envs['MARIADB_PASSWORD']
      end

      envs['APPS_LIST'].split(',').each do |app|
        if envs.has_key?("APPS_LIST__#{app.upcase}__LOGO_PATH")
          envs["APPS_LIST__#{app.upcase}__LOGO_NAME"] = File.basename(envs["APPS_LIST__#{app.upcase}__LOGO_PATH"])
        end
      end

      new_envs
    end

    def run_after_mods(envs)
      return ['traefik']
    end

    def after_start_action(envs)
      envs['APPS_LIST'].split(',').each do |app|
        envs["APPS_LIST__#{app.upcase}__LOGO_NAME"] = File.basename(envs["APPS_LIST__#{app.upcase}__LOGO_PATH"])
        cp_to_container(
          "#{envs['CONTAINER_PREFIX']}llng",
          envs["APPS_LIST__#{app.upcase}__LOGO_PATH"],
          '/usr/share/lemonldap-ng/portal/htdocs/static/common/apps/'
        )
      end
    end

    def docker_services_list
      list = super
      if @envs['KEYCLOAK_ADMINER_ENABLED'] == 'true'
        return list
      else
        return list.minus 'KEYCLOAK_adminer'
      end
    end

    def self.dependent_mods(base_envs)
      if base_envs['KEYCLOAK_ADMINER_ENABLED'] == 'true'
        return ['traefik']
      else
        return ['core']
      end
    end
    
  end
end