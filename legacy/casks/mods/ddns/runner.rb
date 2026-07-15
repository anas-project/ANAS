require 'json'

module Anas
  class DdnsRunner < BaseRunner
    def initialize()
      super
    end

    def self.init
      super
      @required_envs = ['DNS_PROVIDER']
      @optional_envs = [
        'DDNS_DOMAIN_PREFIX'
      ]
      @default_envs = {
        'DDNS_DOMAIN_PREFIX' => 'ddns'
      }
    end

    def self.dependent_mods(base_envs)
      return ['traefik']
    end

    def cal_envs(envs)
      new_envs = envs
      # DNS
      new_envs['DDNS_DNS_SERVER'] = envs['DNS_SERVER'] || '223.5.5.5' unless envs.has_key?('DDNS_DNS_SERVER')
      # TODO add more DNS_PROVIDER
      new_envs['DDNS_DOMAIN'] = "#{envs['DDNS_DOMAIN_PREFIX']}.#{envs['BASE_DOMAIN']}"
      config = { "settings" => [] }
      provider_config = []
      case new_envs['DNS_PROVIDER']
      when 'dnspod'
        dns_config = {
          "provider": 'dnspod',
          "token": new_envs['DNSPOD_API_KEY'],
        }
        if new_envs['IPv4'] == 'true'
          ['@', '*'].each do |host|
            dns_config_new = dns_config.dup
            dns_config_new['domain'] = new_envs['BASE_DOMAIN']
            dns_config_new['host'] = host
            dns_config_new['ip_version'] = 'ipv4'
            config["settings"].push(dns_config_new)
          end
        end

        if new_envs['IPv6'] == 'true'
          ['@', '*'].each do |host|
            dns_config_new = dns_config.dup
            dns_config_new['domain'] = new_envs['BASE_DOMAIN']
            dns_config_new['host'] = host
            dns_config_new['ip_version'] = 'ipv6'
            config["settings"].push(dns_config_new)
          end
        end
      end
      new_envs['DDNS_CONFIG'] = config.to_json
      
      return new_envs
    end
  end
end