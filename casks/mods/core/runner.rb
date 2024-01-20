require 'htauth'
require 'sshkey'
require 'resolv'
require 'ipaddr'
require 'json'
require 'total'

module Anas
  class CoreRunner < BaseRunner
    def initialize()
      super
      @perpare_envs = {} #after deploy_files, cal some envs need to merge
    end

    def self.init
      super
      @required_envs = ['BASE_DOMAIN', 'EMAIL', 'DEFAULT_ROOT_PASSWORD']
      @optional_envs = ['DATA_PATH', 'TZ', 'CONTAINER_PREFIX',
        'IMAGE_PREFIX', 'DEFAULT_SERVICE_ROOT_PASSWORD', 'PUID', 'PGID',
        'BASICAUTH_USER', 'BASICAUTH_PASSWD', 'DEFAULT_LANGUAGE', 'CHINESE_SPEEDUP',
        'USE_DEFAULT_DOMAIN', 'SERVER_NAME',
        # IP
        'HOST_IP',
        # DNS
        'DNS_PROVIDER', 'IPv4', 'IPv6', 'DNS_SERVER',
        # File share
        'USERDATA_PATH', 'USERDATA_NAME',
        'SHARE_DIR_NAME', 'SHARE_GUEST_OK'
        # 'VOLUME_CUSTOM_LIST',
      ]
      @default_envs = {'DATA_PATH' => '~/data', 'TZ' => 'Asia/Hong_Kong', 
        'CONTAINER_PREFIX' => 'anas_', 'IMAGE_PREFIX' => 'anas_',
        'PUID' => 1000, 'PGID' => 1000, 'BASICAUTH_USER' => 'admin',
        'DEFAULT_LANGUAGE' => 'zh-cn', 'CHINESE_SPEEDUP' => 'false',
        'USE_DEFAULT_DOMAIN' => 'yes', 

        # DNS
        'IPv4' => 'true', 'IPv6' => 'true', 'DNS_SERVER' => '223.5.5.5',
        
        'SHARE_DIR_NAME' => 'Share', 'USERDATA_NAME' => 'userdata',
        'SHARE_GUEST_OK' => 'Yes',
      }
      currentDNS = Resolv::DNS::Config.default_config_hash[:nameserver]
      @default_envs['HOST_DNS_SERVER'] = currentDNS.join(' ')
    end
    
    def self.dependent_mods(base_envs)
      return []
    end

    def base_path=(new_base_path)
      super
      perpare_files
    end

    def deploy_files(key, location)
      the_path = @working_path
      # create directory
      path = File.dirname(location)
      unless File.directory?(path)
        FileUtils.mkdir_p(path)
      end
      case key
      when 'ssh_rsa_private'
        FileUtils.cp(File.expand_path("id_rsa", @working_path), location)
      when 'ssh_rsa_public'
        FileUtils.cp(File.expand_path("id_rsa.pub", @working_path), location)
      end
    end

    def perpare_files()
      the_path = @working_path
      k = SSHKey.generate
      rsa_path = File.expand_path("id_rsa", @working_path)
      File.open(rsa_path, 'w') do |file|
        file.write k.private_key
      end
      rsa_pub_path = File.expand_path("id_rsa.pub", @working_path)
      File.open(rsa_pub_path, 'w') do |file|
        file.write k.ssh_public_key
      end
      @perpare_envs['SSH_RSA_PRIVATE'] = k.ssh_public_key
    end

    def cal_envs(envs)
      new_envs = @perpare_envs.merge(envs)
      new_envs['DOCKER_ALPINE_VERSION'] = "3.15"

      if envs.has_key?('SERVER_NAME')
        new_envs['SERVER_NAME'] = envs['SERVER_NAME'].to_s.upcase
      else
        hostname = %x( hostname -s )
        new_envs['SERVER_NAME'] = hostname.strip.to_s.upcase
      end
      
      new_envs['DEFAULT_SERVICE_ROOT_PASSWORD'] = envs['DEFAULT_ROOT_PASSWORD'] unless envs.has_key?('DEFAULT_SERVICE_ROOT_PASSWORD')
      unless envs.has_key?('BASICAUTH_HTPASSWD')
        pass = envs['BASICAUTH_PASSWD'] 
        if pass.nil? || pass.empty?
          pass = new_envs['DEFAULT_SERVICE_ROOT_PASSWORD']
        end
        md5Gen = HTAuth::Md5.new()
        cryptPass = md5Gen.encode(pass)
        # cryptPass.gsub!(/\$/, '$$')
        new_envs['BASICAUTH_HTPASSWD'] = "#{envs['BASICAUTH_USER']}:#{cryptPass}"
      end

      # interface & ips
      ipv4_route_a = JSON.parse((%x( /sbin/ip -j -4 route )).strip)
      new_envs['DEFAULT_GATEWAY_IP'] = ipv4_route_a.find { |ipv4_route| ipv4_route['dst'] == 'default' }['gateway']
      unless envs['HOST_IP']
        ipv4_a = JSON.parse((%x( /sbin/ip -j -4 a )).strip)
        new_envs['INTERFACE'] = ipv4_route_a.find { |ipv4_route| ipv4_route['dst'] == 'default' }['dev']
        ip_lists = ipv4_a.find { |ipv4_item| ipv4_item['ifname'] == new_envs['INTERFACE'] }['addr_info']
        if ip_lists.length < 1
          raise EnvError.new("No IPv4 detected for the DEFAULT INTERFACE.")
        end
        if ip_lists.length > 1
          # Multiple IP detected for the default interface
          new_envs['HOST_IP'] = JSON.parse((%x( /sbin/ip -j -4 route get #{new_envs['DEFAULT_GATEWAY_IP']})).strip).first['prefsrc']
          ip_lists.each do |item|
            if item['local'] == new_envs['HOST_IP']
              new_envs['HOST_SUBNET_MASK'] = item['prefixlen']
              break
            end
          end
        else
          new_envs['HOST_IP'] = ip_lists.first['local']
          new_envs['HOST_SUBNET_MASK'] = ip_lists.first['prefixlen']
        end
      else
        ipv4_a = JSON.parse((%x( /sbin/ip -o -j -4 a )).strip)
        ipv4_a.each do |item|
          ip_info = item['addr_info'].find { |ipv4_route| ipv4_route['local'] == envs['HOST_IP'] }
          unless ip_info.nil?
            new_envs['INTERFACE'] = ip_info['dev']
            new_envs['HOST_SUBNET_MASK'] = ip_info['prefixlen']
            break
          end
        end
        if new_envs['INTERFACE'].nil?
          raise EnvError.new("HOST_IP does not have a specified INTERFACE.")
        end
      end
      # ip_regex = /^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$/
      # if new_envs['HOST_IP'].scan(ip_regex).empty?
      #   raise EnvError.new("The interface: #{envs['INTERFACE']} ip #{new_envs['HOST_IP']} error")
      # end
      # macvlan ip
      begin
        host_ip = IPAddr.new(new_envs['HOST_IP'])
      rescue => exception
        raise NetworkError.new("The interface: #{envs['INTERFACE']} ip: #{new_envs['HOST_IP']} error: #{exception}")
      end
      begin
        host_segment = IPAddr.new("#{new_envs['HOST_IP']}/#{new_envs['HOST_SUBNET_MASK']}")
      rescue => exception
        raise NetworkError.new("The interface: #{envs['INTERFACE']} subnew mask: #{new_envs['HOST_SUBNET_MASK']} error")
      end
      # HOST_SEGMENT: host ip segment eg: 172.16.0.1/16
      new_envs['HOST_SEGMENT'] = "#{host_segment.to_s}/#{new_envs['HOST_SUBNET_MASK']}"
      # HOST_SEGMENT_FULL: host ip segment eg: 172.16.0.1/255.255.0.0
      new_envs['HOST_SEGMENT_FULL'] = host_segment.full_ip_mask
      host_subnet_mask = new_envs['HOST_SUBNET_MASK'].to_i
      vlan_subnet_mask = 28
      new_envs['VLAN_SUBNET_MASK'] = vlan_subnet_mask
      if (28 - host_subnet_mask) <= 0
        raise NetworkError.new("The interface: #{envs['INTERFACE']} ip: #{new_envs['HOST_IP']} error")
      end
      # 
      vlan_segment = IPAddr.new("#{(host_segment ^ ((1 << (32 - host_subnet_mask)) - 1)).to_s}/#{vlan_subnet_mask}")
      new_envs['VLAN_PREFIX'] = vlan_segment.to_s
      # the interface name of vlan bridge
      new_envs['VLAN_BRIDGE_INTERFACE'] = 'anas_bridge'
      new_envs['VLAN_BRIDGE_IP'] = (vlan_segment ^ 1).to_s
      new_envs['VLAN_SEGMENT'] = "#{new_envs['VLAN_PREFIX']}/#{vlan_subnet_mask}"
      # Use host ip as vlan gataway
      new_envs['VLAN_GATEWAY_IP'] = new_envs['HOST_IP']
      new_envs['VLAN_INTERFACE'] = 'anas_macvlan'
      new_envs['LOCAL_DNS_SERVER'] = new_envs['HOST_IP']
      # dirs
      new_envs['USERDATA_PATH'] = File.expand_path(envs['USERDATA_NAME'], envs['DATA_PATH']) unless envs.has_key?('USERDATA_PATH')
      new_envs['DOWNLOAD_DIR_NAME'] = "Downloads"
      new_envs['MUSIC_DIR_NAME'] = "Music"
      new_envs['VIDEO_DIR_NAME'] = "Video"
      return new_envs
    end

    def build
      Log.debug("Core don't need build")
    end

    def start
      Log.debug("Core don't need run docker-compose up")
    end

    def stop
      Log.debug("Core don't need run docker-compose down")
    end
  end
end