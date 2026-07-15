
module Anas
  class LegoRunner
    include BaseRunner
    @@required_envs = ['LEGO_DNS_PROVIDER']
    @@optional_envs = ['LEGO_EMAIL', 'LEGO_DNS_SERVER', 'LEGO_DATA_PATH']
    def initialize()
      super
    end

    def cal_envs(envs)
      new_envs = envs
      new_envs['LEGO_EMAIL'] = envs['EMAIL'] unless envs.has_key?('LEGO_EMAIL')
      new_envs['LEGO_DATA_PATH'] = "#{envs['DATA_PATH']}/lego/certs" unless envs.has_key?('LEGO_DATA_PATH')
      new_envs['LEGO_DNS_SERVER'] = envs['DNS_SERVER'] || '223.5.5.5' unless envs.has_key?('LEGO_DNS_SERVER')
      new_envs['LEGO_CERTS_PATH'] = "#{envs['LEGO_DATA_PATH']}/certificates/"
      new_envs['LEGO_CERT_NAME'] = "#{envs['BASE_DOMAIN']}.crt"
      new_envs['LEGO_KEY_NAME'] = "#{envs['BASE_DOMAIN']}.key"
      new_envs['LEGO_CA_CERT_NAME'] = "#{envs['BASE_DOMAIN']}.issuer.crt"
      return new_envs
    end

  end
end