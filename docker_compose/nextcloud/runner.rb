
module Anas
  class NextcloudRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      # TODO: sso
      @required_envs = [] # TODO
      @optional_envs = [
        'NEXTCLOUD_ADMIN_PASSWORD',
        'NEXTCLOUD_DOMAIN_PREFIX', 'NEXTCLOUD_PHONE_REGION',
        'NEXTCLOUD_ADMIN_USERNAME','NEXTCLOUD_DEFAULT_QUOTA', 'NEXTCLOUD_BASE_PATH', 
        # 'NEXTCLOUD_USER_MIN_PASS_LENGTH', 'NEXTCLOUD_USER_COMPLEX_PASS', 'NEXTCLOUD_USER_MAX_PASS_AGE', 
        'NEXTCLOUD_RM_SKELETON_FILES',
        'NEXTCLOUD_LOG_LEVEL', 'NEXTCLOUD_MEMORY_LIMIT', 'NEXTCLOUD_UPLOAD_MAX_SIZE',
        'NEXTCLOUD_DEBUG', 'NEXTCLOUD_TALK_TURN_PORT', 'NEXTCLOUD_TALK_ENABLED',
        'NEXTCLOUD_MEMORIES_ENABLED',
      ]
      @default_envs = {
        'NEXTCLOUD_DOMAIN_PREFIX' => 'nc', 'NEXTCLOUD_DB_NAME' => 'nextcloud',
        'NEXTCLOUD_PHONE_REGION' => 'CN', 'NEXTCLOUD_RM_SKELETON_FILES' => false,
        'NEXTCLOUD_LOG_LEVEL' => '2', 'NEXTCLOUD_MEMORY_LIMIT' => '1G',
        'NEXTCLOUD_UPLOAD_MAX_SIZE' => '16G', 'NEXTCLOUD_DEBUG' => false,
        'NEXTCLOUD_TALK_TURN_PORT' => 3478, 'NEXTCLOUD_TALK_ENABLED' => true,
        'NEXTCLOUD_MEMORIES_ENABLED' => true,
      }
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['NEXTCLOUD_HOSTNAME'] = "#{new_envs['CONTAINER_PREFIX']}nextcloud"
      new_envs['NEXTCLOUD_BASE_PATH'] = "#{envs['DATA_PATH']}/nextcloud" unless envs.has_key?('NEXTCLOUD_BASE_PATH')
      new_envs['NEXTCLOUD_DOMAIN'] = "#{envs['NEXTCLOUD_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      new_envs['NEXTCLOUD_DOMAIN_PORT'] = "#{envs['NEXTCLOUD_DOMAIN']}:#{envs['TRAEFIK_BASE_PORT']}"
      new_envs['NEXTCLOUD_DOMAIN_FULL'] = "https://#{envs['NEXTCLOUD_DOMAIN_PORT']}"
      
      new_envs['NEXTCLOUD_TALK_TURN_DOMAIN_PORT'] = "#{envs['NEXTCLOUD_DOMAIN']}:#{envs['NEXTCLOUD_TALK_TURN_PORT']}"
      new_envs['NEXTCLOUD_TALK_SIGNALING_DOMAIN_FULL'] = "#{envs['NEXTCLOUD_DOMAIN_FULL']}/talk"

      new_envs['NEXTCLOUD_REDIS_HOSTNAME'] = "#{new_envs['CONTAINER_PREFIX']}nextcloud_redis"
      new_envs['NEXTCLOUD_REDIS_PORT'] = 6379
      unless envs.has_key?('NEXTCLOUD_DB_TYPE')
        if envs.has_key?('POSTGRES_HOST')
          new_envs['NEXTCLOUD_DB_TYPE'] = 'postgres'
        elsif envs.has_key?('MARIADB_HOST')
          new_envs['NEXTCLOUD_DB_TYPE'] = 'mariadb'
        else
          raise EnvError.new("No database for nextcloud.")
        end
      end

      if new_envs['NEXTCLOUD_DB_TYPE'] == 'mariadb'
        new_envs['NEXTCLOUD_NETWORK_DB'] = 'mariadb'
      elsif new_envs['NEXTCLOUD_DB_TYPE'] == 'postgres'
        new_envs['NEXTCLOUD_NETWORK_DB'] = 'postgres'
      end

      new_envs['NEXTCLOUD_IMAGINARY_HOSTNAME'] = "#{new_envs['CONTAINER_PREFIX']}imaginary"
      # avoid conflit with samba admin user
      # TODO: change name
      new_envs['NEXTCLOUD_ADMIN_USERNAME'] = "#{envs['SAMBA_DC_ADMIN_NAME']}_nc" unless envs.has_key?('NEXTCLOUD_ADMIN_USERNAME')
      new_envs['NEXTCLOUD_ADMIN_PASSWORD'] = envs['SAMBA_DC_ADMIN_PASSWORD'] unless envs.has_key?('NEXTCLOUD_ADMIN_PASSWORD')
      if envs['SAMBA_DC_APP_FILTER'] == 'true'
        new_envs['NEXTCLOUD_USER_FILTER'] = "(&#{envs['SAMBA_DC_USER_CLASS_FILTER']}(|(memberOf=CN=APP_nextcloud,#{envs['SAMBA_DC_BASE_APP_DN']})(memberOf=#{envs['SAMBA_DC_APP_ALL_DN']})(memberOf=#{envs['SAMBA_DC_ADMIN_GROUP_DN']})))"
      else
        new_envs['NEXTCLOUD_USER_FILTER'] = "(&#{envs['SAMBA_DC_USER_CLASS_FILTER']})"
      end
      unless envs['NEXTCLOUD_USER_LOGIN_FILTER']
        attrs = envs['SAMBA_DC_USER_LOGIN_ATTRS'].split(',').append('objectGUID')
        uid_filter = "(|#{(attrs.map { |attr| "(#{attr}=%uid)"}).join})"
        new_envs['NEXTCLOUD_USER_LOGIN_FILTER'] = "(&#{new_envs['NEXTCLOUD_USER_FILTER']}#{envs['SAMBA_DC_USER_ENABLED_FILTER']}#{uid_filter})" # don't need $NEXTCLOUD_USER_FILTER ?
      end

      unless envs['NEXTCLOUD_USER_COMPLEX_PASS']
        if envs['SAMBA_DC_USER_COMPLEX_PASS']
          new_envs['NEXTCLOUD_USER_COMPLEX_PASS'] = envs['SAMBA_DC_USER_COMPLEX_PASS']
        else
          new_envs['NEXTCLOUD_USER_COMPLEX_PASS'] = true
        end
      end

      # unless envs['NEXTCLOUD_USER_MAX_PASS_AGE']
      #   if envs['SAMBA_DC_USER_MAX_PASS_AGE']
      #     new_envs['NEXTCLOUD_USER_MAX_PASS_AGE'] = envs['SAMBA_DC_USER_MAX_PASS_AGE']
      #   else
      #     new_envs['NEXTCLOUD_USER_MAX_PASS_AGE'] = 70
      #   end
      # end

      # unless envs['NEXTCLOUD_USER_MIN_PASS_LENGTH']
      #   if envs['SAMBA_DC_USER_MAX_PASS_LENGTH']
      #     new_envs['NEXTCLOUD_USER_MIN_PASS_LENGTH'] = envs['SAMBA_DC_USER_MAX_PASS_LENGTH']
      #   else
      #     new_envs['NEXTCLOUD_USER_MIN_PASS_LENGTH'] = 7
      #   end
      # end
      if envs['SAMBA_DC_APP_FILTER'] == 'true'
        # allow_groups = "APP_nextcloud, #{envs['SAMBA_DC_APP_ALL_NAME']}"
        allow_groups = "APP_nextcloud"
      else
        allow_groups = ""
      end
      new_envs['SMAL_SP_APPS'] = '' unless envs.has_key?('SMAL_SP_APPS')
      saml_apps = new_envs['SMAL_SP_APPS'].split(',')
      saml_apps.push 'nextcloud' unless saml_apps.include? 'nextcloud'
      new_envs['SMAL_SP_APPS'] = saml_apps.join(',')
      new_envs['SMAL_SP__NEXTCLOUD__METADATA_URL'] = "#{new_envs['NEXTCLOUD_DOMAIN_FULL']}/apps/user_saml/saml/metadata?idp=1"
      # var name, attr name, mandatory
      new_envs['SMAL_SP__NEXTCLOUD__ATTR01'] = "cn,cn,1"
      new_envs['SMAL_SP__NEXTCLOUD__ATTR02'] = "sAMAccountName,sAMAccountName,1"
      # new_envs['SMAL_SP__NEXTCLOUD__ATTR03'] = "sAMAccountName,sAMAccountName,1"
      new_envs['SMAL_SP__NEXTCLOUD__NAMEID_FORMAT'] = "windows"
      new_envs['SMAL_SP__NEXTCLOUD__ALLOW_GROUPS'] = allow_groups

      new_envs['APPS_LIST'] = '' unless envs.has_key?('APPS_LIST')
      apps = new_envs['APPS_LIST'].split(',')
      apps.push 'nextcloud' unless apps.include? 'nextcloud'
      new_envs['APPS_LIST'] = apps.join(',')
      new_envs['APPS_LIST__NEXTCLOUD__NAME'] = 'Nextcloud' unless envs.has_key?('APPS_LIST__NEXTCLOUD__NAME')
      new_envs['APPS_LIST__NEXTCLOUD__DESC'] = 'Self hosted file sharing and communication' unless envs.has_key?('APPS_LIST__NEXTCLOUD__DESC')
      if envs.has_key?('APPS_LIST__NEXTCLOUD__LOGO_PATH')
        # TODO: path
        # new_envs['APPS_LIST__NEXTCLOUD__LOGO_PATH'] = 
      else
        new_envs['APPS_LIST__NEXTCLOUD__LOGO_PATH'] = File.join(@working_path, '/assets/nextcloud.png')
      end
      new_envs['APPS_LIST__NEXTCLOUD__URI'] = new_envs['NEXTCLOUD_DOMAIN_FULL']
      new_envs['APPS_LIST__NEXTCLOUD__DOMAIN'] = new_envs['NEXTCLOUD_DOMAIN']
      new_envs['APPS_LIST__NEXTCLOUD__DOMAIN'] = new_envs['NEXTCLOUD_DOMAIN']
      new_envs['APPS_LIST__NEXTCLOUD__ALLOW_GROUPS'] = allow_groups
      return new_envs
    end

    def check_envs(envs)
      super
    end

    def use_ldap?
      return true
    end

    def module_envs(envs)
      new_envs = envs
      # nextcloud
      new_envs['MEMORY_LIMIT'] = envs['NEXTCLOUD_MEMORY_LIMIT']
      new_envs['UPLOAD_MAX_SIZE'] = envs['NEXTCLOUD_UPLOAD_MAX_SIZE']
      new_envs['OPCACHE_MEM_SIZE'] = '128'
      new_envs['APC_SHM_SIZE'] = '128M'
      new_envs['REAL_IP_HEADER'] = 'X-Forwarded-For'
      new_envs['LOG_IP_VAR'] = 'http_x_forwarded_for'
      
      new_envs['HSTS_HEADER'] = 'max-age=15768000; includeSubDomains'
      new_envs['RP_HEADER'] = 'strict-origin'
      new_envs['SUBDIR'] = ''

      if envs['NEXTCLOUD_DB_TYPE'] == 'postgres'
        new_envs['DB_HOST'] = envs['POSTGRES_HOST_PORT']
        new_envs['DB_USER'] = envs['POSTGRES_USERNAME']
        new_envs['DB_PASSWORD'] = envs['POSTGRES_PASSWORD']
        new_envs['DB_TYPE'] = 'pgsql'
      elsif envs['NEXTCLOUD_DB_TYPE'] == 'mariadb'
        new_envs['DB_HOST'] = envs['MARIADB_HOST_PORT']
        new_envs['DB_USER'] = envs['MARIADB_USERNAME']
        new_envs['DB_PASSWORD'] = envs['MARIADB_PASSWORD']
        new_envs['DB_TYPE'] = 'mysql'
      end
      new_envs['DB_NAME'] = envs['NEXTCLOUD_DB_NAME']

      new_envs['TALK_TURN_SECRET'] = String.random_password(32)
      new_envs['TALK_SIGNALING_SECRET'] = String.random_password(32)

      return new_envs
    end

    def self.dependent_mods(base_envs)
      return ['traefik']
    end

    def run_after_mods(envs)
      return ['samba_dc', 'postgres', 'mariadb']
    end

    def services_list
      list = super
      if @envs['NEXTCLOUD_TALK_ENABLED'] == 'true'
        return list
      else
        return list.minus 'talk'
      end
    end

  end
end