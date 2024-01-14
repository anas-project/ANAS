require 'openssl'

module Anas
  class LlngRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = []
      @optional_envs = [
        'LLNG_DOMAIN_PREFIX', 'LLNG_MANAGER_DOMAIN_PREFIX', 'LLNG_TEST_DOMAIN_PREFIX',
        'LLNG_LOG_LEVEL',
        'LLNG_DB_TYPE', 'LLNG_ENABLE_TEST',
      ]
      @default_envs = {
        'LLNG_DOMAIN_PREFIX' => 'auth', 'LLNG_MANAGER_DOMAIN_PREFIX' => 'auth-manager',
        'LLNG_TEST_DOMAIN_PREFIX' => 'auth-test',
        'LLNG_LOG_LEVEL' => 'warn', 'LLNG_DB_NAME' => 'lemonldap-ng',
        'LLNG_ENABLE_TEST' => true,
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['LLNG_PASSWORD'] = envs['DEFAULT_SERVICE_ROOT_PASSWORD'] unless envs.has_key?('LLNG_PASSWORD')
      new_envs['LLNG_DOMAIN'] = "#{envs['LLNG_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['LLNG_DOMAIN_PORT'] = "#{envs['LLNG_DOMAIN']}:#{envs['TRAEFIK_BASE_PORT']}"
      new_envs['LLNG_DOMAIN_FULL'] = "https://#{envs['LLNG_DOMAIN_PORT']}"

      new_envs['LLNG_TEST_DOMAIN'] = "#{envs['LLNG_TEST_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['LLNG_TEST_DOMAIN_PORT'] = "#{envs['LLNG_TEST_DOMAIN']}:#{envs['TRAEFIK_BASE_PORT']}"
      new_envs['LLNG_TEST_DOMAIN_FULL'] = "https://#{envs['LLNG_TEST_DOMAIN_PORT']}"

      new_envs['LLNG_MANAGER_DOMAIN'] = "#{envs['LLNG_MANAGER_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['LLNG_MANAGER_DOMAIN_PORT'] = "#{envs['LLNG_MANAGER_DOMAIN']}:#{envs['TRAEFIK_BASE_PORT']}"
      new_envs['LLNG_MANAGER_DOMAIN_FULL'] = "https://#{envs['LLNG_MANAGER_DOMAIN_PORT']}"
      new_envs['LLNG_HOST'] = 'llng'
      new_envs['LLNG_HOST_PORT'] = "#{new_envs['LLNG_HOST']}:#{new_envs['LLNG_PORT']}"

      new_envs['LLNG_HOST'] = 'llng'
      new_envs['LLNG_HANDLER_SOCKET_PORT'] = '9000'

      unless envs.has_key?('LLNG_DB_TYPE')
        if envs.has_key?('POSTGRES_HOST')
          new_envs['LLNG_DB_TYPE'] = 'postgres'
        elsif envs.has_key?('MARIADB_HOST')
          new_envs['LLNG_DB_TYPE'] = 'mariadb'
        else
          raise EnvError.new("No database for lemonldap-ng.")
        end
      end

      if new_envs['LLNG_DB_TYPE'] == 'mariadb'
        new_envs['LLNG_NETWORK_DB'] = 'mariadb'
      elsif new_envs['LLNG_DB_TYPE'] == 'postgres'
        new_envs['LLNG_NETWORK_DB'] = 'postgres'
      end

      new_envs['LLNG_LDAP_AUTH_FILTER'] = "(&#{new_envs['SAMBA_DC_USER_CLASS_FILTER']}#{envs['SAMBA_DC_USER_ENABLED_FILTER']}(#{envs['SAMBA_DC_USER_NAME']}=$user))"
      new_envs['LLNG_LDAP_MAIL_FILTER'] = "(&#{new_envs['SAMBA_DC_USER_CLASS_FILTER']}#{envs['SAMBA_DC_USER_ENABLED_FILTER']}(#{envs['SAMBA_DC_USER_EMAIL']}=$mail))"
      
      # SAML & OIDC service Signature
      rsa_key = OpenSSL::PKey::RSA.new(2048)
      cert = OpenSSL::X509::Certificate.new
      cert.version = 2
      cert.subject = OpenSSL::X509::Name.parse("/CN=#{new_envs['LLNG_DOMAIN']}")
      cert.issuer = cert.subject
      cert.public_key = rsa_key.public_key
      cert.not_before = Time.now
      cert.not_after = cert.not_before + 3650 * 24 * 60 * 60
      cert.sign(rsa_key, OpenSSL::Digest::SHA256.new)
      new_envs['LLNG_SAML_SERVICE_PRIVATE_KEY'] = rsa_key.to_pem.inspect
      new_envs['LLNG_SAML_SERVICE_PUBLIC_KEY'] = cert.to_pem.inspect
      new_envs['LLNG_OIDC_SERVICE_PRIVATE_KEY'] = rsa_key.to_pem.inspect
      new_envs['LLNG_OIDC_SERVICE_PUBLIC_KEY'] = cert.to_pem.inspect
      new_envs['LLNG_OIDC_SERVICE_KEY_ID'] = String.random(12)

      # SAML URI
      new_envs['LLNG_SAML_IDP_ENTITY_ID'] = "#{new_envs['LLNG_DOMAIN_FULL']}/saml/metadata"
      new_envs['LLNG_SAML_IDP_SSO'] = "#{new_envs['LLNG_DOMAIN_FULL']}/saml/singleSignOn"
      new_envs['LLNG_SAML_IDP_SLO'] = "#{new_envs['LLNG_DOMAIN_FULL']}/saml/singleLogout"
      new_envs['LLNG_SAML_IDP_SLO_RESPONSE'] = "#{new_envs['LLNG_DOMAIN_FULL']}/saml/singleLogoutReturn"

      return new_envs
    end
    
    def module_envs(envs)
      new_envs = envs
      # not use privilege user
      if envs['LLNG_DB_TYPE'] == 'postgres'
        new_envs['DB_HOST'] = envs['POSTGRES_HOST']
        new_envs['DB_POST'] = envs['POSTGRES_PORT']
        new_envs['DB_USER'] = envs['POSTGRES_USERNAME']
        new_envs['DB_PASSWORD'] = envs['POSTGRES_PASSWORD']
      elsif envs['LLNG_DB_TYPE'] == 'mariadb'
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
      if @envs['LLNG_ADMINER_ENABLED'] == 'true'
        return list
      else
        return list.minus 'LLNG_adminer'
      end
    end

    def self.dependent_mods(base_envs)
      if base_envs['LLNG_ADMINER_ENABLED'] == 'true'
        return ['traefik']
      else
        return ['core']
      end
    end
    
  end
end