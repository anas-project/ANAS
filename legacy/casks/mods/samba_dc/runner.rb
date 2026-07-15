module Anas
  class SambaDcRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = []
      @optional_envs = [
        'SAMBA_DC_ADMIN_PASSWORD', 'SAMBA_DC_ADMINISTRATOR_PASSWORD',
        'SAMBA_DC_REALM', 'SAMBA_DC_WORKGROUP', 
        'SAMBA_DC_SERVER_STRING', 'SAMBA_DC_NETBIOS_NAME', 'SAMBA_DC_INTERFACES',
        'SAMBA_DC_APP_FILTER', 'SAMBA_DC_CREATE_STRUCTURE',
        'SAMBA_DC_ADMIN_NAME', 'SAMBA_DC_TEMPLATE_SHELL', 'SAMBA_DC_TEMPLATE_HOMEDIR',
        'SAMBA_DC_DOMAIN_USERS_GID_NUMBER', 'SAMBA_DC_USER_COMPLEX_PASS', 'SAMBA_DC_USER_MAX_PASS_AGE',
        'SAMBA_DC_USER_MAX_PASS_LENGTH', 'SAMBA_DC_LOG_LEVEL', 'SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN',
      ]
      @default_envs = {'SAMBA_DC_APP_FILTER' => 'false', 'SAMBA_DC_CREATE_STRUCTURE' => 'true',
        'SAMBA_DC_ADMIN_NAME' => 'admin', 'SAMBA_DC_TEMPLATE_SHELL' => '/bin/false',
        'SAMBA_DC_TEMPLATE_HOMEDIR' => '/home/%D/%U', 'SAMBA_DC_DOMAIN_USERS_GID_NUMBER' => 10000,
        'SAMBA_DC_USER_COMPLEX_PASS' => true, 'SAMBA_DC_USER_MAX_PASS_AGE' => 70, 
        'SAMBA_DC_USER_MIN_PASS_LENGTH' => 7, 'SAMBA_DC_LOG_LEVEL' => 1,
      }
    end

    def cal_envs(envs)
      new_envs = envs
      ensure_env!(envs, 'BASE_DOMAIN')
      new_envs['SAMBA_DC_DOMAIN'] = envs['BASE_DOMAIN']
      new_envs['SAMBA_DC_DNS_SEARCH'] = new_envs['SAMBA_DC_DOMAIN']
      if envs.has_key?('SAMBA_DC_REALM')
        new_envs['SAMBA_DC_REALM'] = envs['SAMBA_DC_REALM'].to_s.upcase
      else
        new_envs['SAMBA_DC_REALM'] = envs['BASE_DOMAIN'].to_s.upcase
      end
      if envs.has_key?('SAMBA_DC_NETBIOS_NAME')
        new_envs['SAMBA_DC_NETBIOS_NAME'] = envs['SAMBA_DC_NETBIOS_NAME'].to_s.upcase
      else
        new_envs['SAMBA_DC_NETBIOS_NAME'] = envs['SERVER_NAME']
      end
      new_envs['SAMBA_DC_DC_NAME'] = new_envs['SAMBA_DC_NETBIOS_NAME'].to_s.downcase
      new_envs['SAMBA_DC_DC_DOMAIN'] = "#{new_envs['SAMBA_DC_DC_NAME']}.#{new_envs['SAMBA_DC_DOMAIN']}"
      new_envs['SAMBA_DC_ADMINISTRATOR_NAME'] = 'Administrator'
      new_envs['SAMBA_DC_ADMIN_DISPLAY_NAME'] = 'Administrator'
      new_envs['SAMBA_DC_ADMIN_PASSWORD'] = envs['DEFAULT_ROOT_PASSWORD'] unless envs.has_key?('SAMBA_DC_ADMIN_PASSWORD')
      new_envs['SAMBA_DC_ADMINISTRATOR_PASSWORD'] = envs['DEFAULT_ROOT_PASSWORD'] unless envs.has_key?('SAMBA_DC_ADMINISTRATOR_PASSWORD')
      new_envs['SAMBA_DC_LDAPS_SERVER_URL'] = "ldaps://#{envs['BASE_DOMAIN']}" unless envs.has_key?('SAMBA_DC_SERVER_FULL_URL')
      new_envs['SAMBA_DC_HOST'] = envs['BASE_DOMAIN']
      new_envs['SAMBA_DC_LDAPS_PORT'] = "636"
      new_envs['SAMBA_DC_LDAPS_SERVER_URL_PORT'] = "#{envs['SAMBA_DC_LDAPS_SERVER_URL']}:#{envs['SAMBA_DC_LDAPS_PORT']}"
      domain = new_envs['SAMBA_DC_DOMAIN']
      new_envs['SAMBA_DC_WORKGROUP'] = domain.split('.').first unless envs.has_key?('SAMBA_DC_WORKGROUP')
      new_envs['SAMBA_DC_WORKGROUP'] = new_envs['SAMBA_DC_WORKGROUP'].to_s.upcase
      new_envs['SAMBA_DC_BASE_DN'] = 'DC=' + domain.split('.').join(',DC=')
      new_envs['SAMBA_DC_BASE_COMPUTERS_DN'] = "CN=Computers,#{new_envs['SAMBA_DC_BASE_DN']}"
      new_envs['SAMBA_DC_BASE_GROUPS_DN'] = "OU=Groups,#{new_envs['SAMBA_DC_BASE_DN']}"
      new_envs['SAMBA_DC_BASE_GROUPS_ROLE_DN'] = "OU=Role,#{new_envs['SAMBA_DC_BASE_GROUPS_DN']}"
      new_envs['SAMBA_DC_BASE_USERS_DN_NAME'] = "People"
      new_envs['SAMBA_DC_BASE_USERS_DN_PREFIX'] = "OU=#{new_envs['SAMBA_DC_BASE_USERS_DN_NAME']}"
      new_envs['SAMBA_DC_BASE_USERS_DN'] = "#{new_envs['SAMBA_DC_BASE_USERS_DN_PREFIX']},#{new_envs['SAMBA_DC_BASE_DN']}"
      new_envs['SAMBA_DC_BASE_APP_DN'] = "OU=Apps,#{new_envs['SAMBA_DC_BASE_GROUPS_DN']}"
      new_envs['SAMBA_DC_APP_ALL_NAME'] = 'APP_all' #can access all app when SAMBA_DC_APP_FILTER == true
      new_envs['SAMBA_DC_APP_ALL_DN'] = "CN=#{envs['SAMBA_DC_APP_ALL_NAME']},#{envs['SAMBA_DC_BASE_APP_DN']}"
      new_envs['SAMBA_DC_ADMINISTRATOR_DN'] = "CN=#{new_envs['SAMBA_DC_ADMINISTRATOR_NAME']},CN=Users,#{new_envs['SAMBA_DC_BASE_DN']}"
      new_envs['SAMBA_DC_ADMIN_DN'] = "CN=#{new_envs['SAMBA_DC_ADMIN_NAME']},#{new_envs['SAMBA_DC_BASE_USERS_DN']}"
      new_envs['SAMBA_DC_ADMIN_GROUP_NAME'] = "Admins"
      new_envs['SAMBA_DC_ADMIN_GROUP_DN'] = "CN=#{new_envs['SAMBA_DC_ADMIN_GROUP_NAME']},#{new_envs['SAMBA_DC_BASE_GROUPS_ROLE_DN']}"
      new_envs['SAMBA_DC_GROUP_CLASS_NAME'] = "group"
      new_envs['SAMBA_DC_GROUP_CLASS_FILTER'] = "(objectClass=group)"
      new_envs['SAMBA_DC_USER_CLASS_NAME'] = "user"
      new_envs['SAMBA_DC_USER_CLASS_FILTER'] = "(objectClass=user)"
      new_envs['SAMBA_DC_USER_ENABLED_FILTER'] = "(!(userAccountControl:1.2.840.113556.1.4.803:=2))"
      new_envs['SAMBA_DC_USER_LOGIN_ATTRS'] = 'sAMAccountName,userPrincipalName,mail' unless envs.has_key?('SAMBA_DC_USER_LOGIN_ATTRS')
      new_envs['SAMBA_DC_USER_NAME'] = 'sAMAccountName'
      new_envs['SAMBA_DC_USER_DISPLAY_NAME'] = 'displayName'
      new_envs['SAMBA_DC_GROUP_DISPLAY_NAME'] = 'name'
      new_envs['SAMBA_DC_GROUP_MEMBER_ATTR'] = 'member'
      new_envs['SAMBA_DC_USER_EMAIL'] = 'mail'
      new_envs['SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN'] = envs['BASE_DOMAIN'] unless envs.has_key?('SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN')
      new_envs['SAMBA_DC_INTERFACES'] = envs['INTERFACE'] unless envs.has_key?('SAMBA_DC_INTERFACES')
      return new_envs
    end

    def check_envs(envs)
      if envs['SAMBA_DC_ADMIN_NAME'] == envs['SAMBA_DC_ADMINISTRATOR_NAME']
        raise EnvError.new("Samba admin user name can't be #{envs['SAMBA_DC_ADMINISTRATOR_NAME']}")
      end
      super
    end

    def module_envs(envs)
      new_envs = envs
      new_envs['KRB5RCACHETYPE'] = 'none'
      return new_envs
    end

    def render_files!(envs)
      file_path = File.expand_path("samba_dc/root/root/.ssh/authorized_keys", @working_path)
      FileUtils.mkdir_p(File.dirname(file_path))
      File.open(file_path, 'w') do |file|
        file.write envs['SSH_RSA_PRIVATE']
      end
      super
    end
    
    def self.dependent_mods(base_envs)
      return ['lego', 'bind']
    end

  end
end
