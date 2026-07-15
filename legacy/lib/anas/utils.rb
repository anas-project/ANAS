require 'logger'
require 'yaml'
require 'pathname'
require 'ipaddr'

# The base class & method for A NAS
module Anas
  class NoENVError < StandardError; end
  class PermissionError < StandardError; end
  class LoadModuleError < StandardError; end
  class ConfigError < StandardError; end
  class NotInstalledError < StandardError; end
  class HasDependencyError < StandardError; end
  class EnvError < StandardError; end
  class NetworkError < StandardError; end
  class UnknownError < StandardError; end
  class ModConflictError < StandardError; end

  # Loggers, default level is `info`
  Log = Logger.new(STDOUT)

  @@docker_compose_cmd = nil

  def self.docker_compose_cmd; @@docker_compose_cmd end
  def self.docker_compose_cmd= v; @@docker_compose_cmd = v end

  class ModNode
    # @return [Array<String>] list of dependent mods
    attr_accessor :dependent_nodes

    # @return [Array<String>] list of been dependent mods
    attr_accessor :dependency_nodes

    # @return [String] the module name
    attr_accessor :mod_name

    # # @return bool 
    # attr_reader :is_running

    # @return object the runner of the mod
    attr_reader :runner

    def initialize(runner)
      @runner = runner
      @mod_name = runner.mod_name
      @dependency_nodes = {}
      @dependent_nodes = {}
    end

    def is_running?
      @is_start = @runner.is_running?
    end

    def has_dependency?
      return @dependency_nodes.length != 0
    end

    def add_dependency(mod_name, mod_node)
      unless @dependency_nodes.keys.include? mod_name
        @dependency_nodes[mod_name] = mod_node
        return mod_name
      end
      return nil
    end

    def rm_dependency(mod_name)
      if @dependency_nodes.keys.include? mod_name
        @dependency_nodes.delete mod_name
        return mod_name
      end
      return nil
    end

    def add_dependent(mod_name, mod_node)
      unless @dependent_nodes.keys.include? mod_name
        @dependent_nodes[mod_name] = mod_node
        return mod_name
      end
      return nil
    end

    def rm_dependent(mod_name)
      if @dependent_nodes.keys.include? mod_name
        @dependent_nodes.delete mod_name
        return mod_name
      end
      return nil
    end

    def inspect
      return "<ModNode @ #{@mod_name}>"
    end
  end

  class DependentTree
    attr_accessor :mod_names
    attr_accessor :all_dependent_nodes

    def mod_runner!(mod)
      return @runner_classes[mod] if @runner_classes[mod]
      begin
        runner = mod.mod_class!.new
        @runner_classes[mod] = runner
        runner.docker_compose_cmd = Anas.docker_compose_cmd
        runner.core_runner = @runner_classes['core']
        return runner
      rescue => exception
        Log.error("Load module #{mod} exception: #{exception}")
        raise LoadModuleError.new(mod)
      end
    end
    
    def initialize(mod_names, config_envs)
      @mod_names = mod_names
      @all_dependent_nodes = {}
      @runner_classes = {}
      @working_path = nil
      build_dependent(mod_names, config_envs)
    end

    def build_dependent(mod_names, config_envs)
      @config_envs = config_envs
      def build_node_dependent(mod_name) 
        # get tree node cache
        dependent_node = @all_dependent_nodes[mod_name]
        return dependent_node if dependent_node
        # get mod cache
        mod_runner = mod_runner!(mod_name)
        # create tree node
        mod_node = ModNode.new(mod_runner)
        @all_dependent_nodes[mod_name] = mod_node
        new_envs = mod_runner.class.default_envs.merge(@config_envs)
        mod_runner.class.dependent_mods(new_envs).each do |dep_mod_name|
          # build dependent tree node
          dependent_mod_node = build_node_dependent(dep_mod_name)
          # add dependent vector
          mod_node.add_dependent(dep_mod_name, dependent_mod_node)
          dependent_mod_node.add_dependency(mod_name, mod_node)
        end
        return mod_node
      end

      mod_names.each do |mod_name|
        build_node_dependent(mod_name)
      end

      def deal_run_after_mods(mod_name)
        c_node = @all_dependent_nodes[mod_name]
        runner = c_node.runner
        new_envs = runner.class.default_envs.merge(@config_envs)
        run_after_mod_name = runner.run_after_mods(new_envs)
        run_after_mod_name.each do |after_mod_name|
          after_node = @all_dependent_nodes[after_mod_name]
          # Add dependent relationship only when after_node exist
          unless after_node.nil?
            # add dependent vector
            c_node.add_dependent(after_mod_name, after_node)
            after_node.add_dependency(mod_name, c_node)
          end
        end
      end

      all_mods_name.each do |mod_name|
        deal_run_after_mods(mod_name)
      end
      
      def deal_run_before_mods(mod_name)
        c_node = @all_dependent_nodes[mod_name]
        runner = c_node.runner
        new_envs = runner.class.default_envs.merge(@config_envs)
        run_before_mod_name = runner.run_before_mods(new_envs)
        run_before_mod_name.each do |before_mod_name|
          before_node = @all_dependent_nodes[before_mod_name]
          # Add dependent relationship only when before_node exist
          unless before_node.nil?
            # add dependent vector
            c_node.add_dependency(before_mod_name, before_node)
            before_node.add_dependent(mod_name, c_node)
          end
        end
      end

      all_mods_name.each do |mod_name|
        deal_run_before_mods(mod_name)
      end
    end

    def reset!
      @runner_classes = {}
    end

    def set_working_path(working_path)
      @working_path = working_path
      @runner_classes.values.each do |runner|
        runner.base_path = working_path
      end
    end

    def root_node
      return @all_dependent_nodes['core']
    end

    def rm_node!(mod_name)
      mod_node = @all_dependent_nodes[mod_name]
      if mod_node.has_dependency?
        d_nodes_name = []
        mod_node.dependency_nodes.keys.each do |n|
          d_nodes_name.append(n.mod_name)
        end
        raise HasDependencyError.new("#{mod_name} dependency_nodes #{d_nodes_name}")
      end
      mod_node.dependent_nodes.values.each do |node|
        node.rm_dependency(mod_name)
      end
      @all_dependent_nodes.delete mod_name
    end

    def all_mods_name
      return @all_dependent_nodes.keys
    end

    def ldap_mods_name
      mods_name =[]
      @all_dependent_nodes.each do |key, node|
        if node.runner.use_ldap?
          mods_name.append key
        end
      end
      return mods_name
    end

    # @return [Hash<String, Array<String>>] Hash key are 'required' & 'optional'
    def use_host_lan_mods_name
      required_mods_name = []
      optional_mods_name = []
      @all_dependent_nodes.each do |key, node|
        if node.runner.use_host_lan? == 'required'
          required_mods_name.append key
        elsif node.runner.use_host_lan? == 'optional'
          optional_mods_name.append key
        end
      end
      return {
        'required' => required_mods_name,
        'optional' => optional_mods_name,
      }
    end

    def domains(envs)
      domains = []
      @all_dependent_nodes.each do |key, node|
        domain = node.runner.domain(envs)
        if !domain.nil?
          domains += domain
        end
      end
      return domains
    end

    def require_host_lan_mods_name
      return use_host_lan_mods_name['required'].length != 0
    end

    # Process mod by dependent order
    # block return value can't use `return`
    # 
    # @param mods_name [Array<String>] mods name
    # @param code [Proc] the code to process
    def process_mods(mods_name, init_result = nil, &block)
      mods_name = all_mods_name if mods_name.nil?
      @done_mods = []
      @result = init_result
      def process(mod_name, &block)
        return if @done_mods.include?(mod_name)
        c_node = @all_dependent_nodes[mod_name]
        c_node.dependent_nodes.each do |key, node|
          process(node.mod_name, &block)
        end
        @result = block.call(mod_name, c_node, @result)
        @done_mods.append(mod_name)
      end
      mods_name.each do |mod_name| 
        process(mod_name, &block)
      end
      return @result
    end

    # Process mod by dependency order, reverse dependent order
    # 
    # @param mods_name [Array<String>] mods name
    # @param code [Proc] the code to process
    def reverse_process_mods(mods_name, init_result = nil, &block)
      mods_name = all_mods_name if mods_name.nil?
      @reverse_done_mods = []
      @result = init_result
      def process(mod_name, &block)
        return if @reverse_done_mods.include?(mod_name)
        c_node = @all_dependent_nodes[mod_name]
        c_node.dependency_nodes.each do |d_name, d_node|
          process(d_name, &block)
        end
        @result = block.call(mod_name, c_node, @result)
        @reverse_done_mods.append(mod_name)
      end
      mods_name.each do |mod_name| 
        process(mod_name, &block)
      end
      return @result
    end
  end

  class Starter
    # init starter
    # 
    # @param actions [Array<String>] action need to be excute
    # @param options [Hash<String, Any>] the config.yml file, have mods, envs child hash
    def initialize(actions, options)
      Log.info("Log level set to #{options[:log_level]}")
      Log.level = options[:log_level]
      @options = options
      @actions = actions

      # start
      @dependent_tree = nil

      @base_path = nil

      # init_mods_class
      check_system_envs
    end

    # def init_mods_class
    #   names = Anas.constants.select do |name|
    #     Anas.const_get(name).instance_of?(Class) && name.to_s.end_with?('Runner') && name != :BaseRunner
    #   end
    #   names.each { |n| n.init }
    # end

    def reset_runtime
      @dependent_tree = nil
      @config = nil
    end

    def write_config!(config, path)
      File.open(path, 'w') do |f|
        f.write config.to_yaml
      end
      Log.info("Write config to #{path}")
    end

    def check_system_envs # check tools
      Log.info("Checking your system...")
      Log.info("Checking Docker...")
      docker_result = 'docker -v'.cmd_exist
      if docker_result
        version = /version (\S+)/.match(docker_result)[1]
        Log.info("docker version: #{version}")
      else
        raise NotInstalledError.new("Docker not install")
      end
      dockercompose_result = 'docker compose version'.cmd_exist
      if dockercompose_result
        version = /version (\S+)/.match(dockercompose_result)[1]
        Anas.docker_compose_cmd = 'docker compose'
        Log.info("Use `docker compose` version: #{version}")
      else
        docker_compose_result = 'docker-compose -v'.cmd_exist
        if docker_compose_result
          version = /version ([\d.]+)/.match(docker_compose_result)[1]
          Anas.docker_compose_cmd = 'docker-compose'
          Log.info("Use `docker-compose` version: #{version}")
        else
          raise NotInstalledError.new("Docker compose not install")
        end
      end
    end

    def get_all_mod_names
      names = Anas.constants.select do |name|
        Anas.const_get(name).instance_of?(Class) && name.to_s.end_with?('Runner') && name != :BaseRunner
      end
      return names.map { |n| n.to_s }
    end

    def check_envs(mods, envs)
      Log.info("Checking envs")
      dependent_tree = @dependent_tree
      return dependent_tree.process_mods(mods, {}) do |mod_name, node, missing_envs|
        runner = node.runner
        missing = runner.check_envs(envs)
        missing_envs[mod_name] = missing unless missing.empty?
        missing_envs
      end
    end

    def cal_envs(mods, envs)
      Log.info("Calculate the envs")
      dependent_tree = @dependent_tree
      return dependent_tree.process_mods(mods, envs) do |mod_name, node, envs|
        runner = node.runner
        new_envs = runner.cal_envs(envs)
        new_envs
      end
    end

    def gen_files(mods, envs)
      Log.info("Genarate files by envs")
      dependent_tree = @dependent_tree
      return dependent_tree.process_mods(mods) do |mod_name, node|
        runner = node.runner
        envs_clone = envs.clone
        runner.envs = envs_clone.value_to_string!
        runner.gen_files(runner.envs)
      end
    end

    def start_mods(mods, envs)
      Log.info("Start modules")
      dependent_tree = @dependent_tree
      if dependent_tree.require_host_lan_mods_name
        init_network(envs)
      end
      dependent_tree.process_mods(mods) do |mod_name, node|
        node.runner.start
      end
    end

    def stop_mods(mods)
      Log.info("Stop modules")
      dependent_tree = @dependent_tree
      dependent_tree.reverse_process_mods(mods) do |mod_name, node|
        node.runner.stop
      end
    end

    def build_mods(mods, envs)
      Log.info("Start build modules")
      dependent_tree = @dependent_tree
      dependent_tree.process_mods(mods) do |mod_name, node|
        node.runner.build
      end
    end

    # The module default envs + config_envs
    # 
    # @param mods [Array<String>] list of mods
    # @param config_envs [Hash<String, String>] config file content
    # @return [Hash<String, String>] default envs
    def get_static_envs(mods, config_envs)
      dependent_tree = @dependent_tree
      default_envs = dependent_tree.process_mods(mods, {}) do |mod_name, node, envs|
        envs.merge node.runner.class.default_envs
      end
      static_envs = default_envs.update(config_envs)
      static_envs["ALL_MODS_NAME"] = mods.join(',')
      return static_envs.value_to_string! # env value must string
    end

    # Calculate the envs, build dependent tree, & set envs to runner
    # 
    # @return [Hash<String, String>] full envs
    def process_envs(mods, static_envs)
      dependent_tree = @dependent_tree
      static_envs["USE_LDAP_MODS_NAME"] = dependent_tree.ldap_mods_name.join(',')
      use_host_lan_mods_name = dependent_tree.use_host_lan_mods_name
      static_envs["USE_HOST_LAN_REQUIRED_MODS_NAME"] = use_host_lan_mods_name['required'].join(',')
      static_envs["USE_HOST_LAN_OPTIONAL_MODS_NAME"] = use_host_lan_mods_name['optional'].join(',')
      envs = cal_envs(mods, static_envs)
      Log.debug("Calculate envs is \n #{envs}")
      missing_envs = check_envs(mods, envs)
      unless missing_envs.empty?
        Log.error("Missing envs\n#{missing_envs}")
        raise NoENVError.new(missing_envs)
      end
      domains = dependent_tree.domains(envs)
      envs["DOMAINS"] = domains.map { |domain| domain.join('/') }.join(',')
      # dependent_tree.process_mods(mods) do |mod_name, node|
      #   runner = node.runner
      #   runner.append_envs(new_envs)
      # end
      gen_files(mods, envs)
      return envs
    end

    def sync_tmp!(tmp,release)
      if File.directory? release
        FileUtils.rm_rf(release)
      end
      FileUtils.mv(tmp, release)
    end

    def clean_tmp!(tmp)
      FileUtils.rm_rf(tmp)
    end

    # load & check config file
    # 
    # @return [Hash<String, Any>], the checked config
    def load_config(config_path)
      if config_path.nil? || !File.file?(config_path)
        raise ConfigError.new("No config file in #{config_path}")
      end
      Log.info("Load config path #{config_path}")
      config = YAML.load_file(config_path)
      check_config(config)
      return config
    end

    def check_config(config)
      unless config['mods'] || config['mods'].is_a?(Array)
        raise ConfigError.new("No `modules` in config file")
      end

      unless config['envs'] || config['envs'].is_a?(Hash)
        raise ConfigError.new("No `envs` in config file")
      end
    end

    # Config docker, /etc/docker/daemon.json, 
    # add IPv6 support.
    def config_docker(envs)
      base_path = @base_path
      Log.info("Render anas_service.sh.erb")
      JSON.load_file("/etc/docker/daemon.json")
    end

    # Create macvlan network
    # Create macvlan network bridge & set it launch at login
    def init_network(envs)
      base_path = @base_path
      Log.info("Render anas_service.sh.erb")
      service_erb_path = File.expand_path('anas_service.sh.erb', __dir__)
      bridge_interface = envs['VLAN_BRIDGE_INTERFACE']
      render_envs = {
        'default_interface' => envs['INTERFACE'],
        'ip_addr' => envs['VLAN_BRIDGE_IP'],
        'subnet_mask' => envs['VLAN_SUBNET_MASK'],
        'netwrok_prefix' => envs['VLAN_PREFIX'],
        'bridge_interface' => bridge_interface,
      }
      Log.debug("Render envs #{render_envs}")
      rendered = ERB.new(File.read(service_erb_path), nil).result_with_hash(render_envs)
      rendered_file = File.expand_path('anas_service.sh', base_path)
      File.open(rendered_file, 'w') { |file| file.write(rendered) }
      FileUtils.chmod("+x", rendered_file)
      Log.info("Add crontab @reboot #{rendered_file}")
      print "Add macblan network bridge launch at login need root privillege \n"
      %x(sudo sh -c '(crontab -l ; echo "@reboot #{rendered_file}") | sort - | uniq - | crontab - ')
      print "\n"
      error_code = $?.exitstatus
      if error_code != 0
        raise NetworkError.new("Add macvlan network bridge launch at login failed error_code: #{error_code}")
      end
      Log.info("Check macvlan network bridge whether exist?")
      result = %x(ip link show #{bridge_interface})
      unless result.include?('does not exist')
        unless "Interface `#{bridge_interface}` is exist, ANAS will delete it before create bridge, Continue?".yesno?
          exit!
        end
        Log.info("sudo ip link delete #{bridge_interface}")
        %x(sudo ip link delete #{bridge_interface})
        error_code = $?.exitstatus
        if error_code != 0
          raise NetworkError.new("Delete interface `#{bridge_interface}` failed error_code: #{error_code}")
        end
      end
      Log.info("Create macvlan network bridge, execute: 'sudo sh #{rendered_file}'")
      %x(sudo sh #{rendered_file})
      # docker network
      vlan_interface = envs['VLAN_INTERFACE']
      result = %x(docker network ls -f 'name=#{vlan_interface}')
      if result.include?(vlan_interface)
        unless "Docker network `#{vlan_interface}` is exist, ANAS will delete it before create network, Continue?".yesno?
          exit!
        end
        Log.info("docker network rm #{vlan_interface}")
        %x(docker network rm #{vlan_interface})
        error_code = $?.exitstatus
        if error_code != 0
          raise NetworkError.new("Delete docker network `#{vlan_interface}` failed error_code: #{error_code}")
        end
      end
      Log.info("Create docker macvlan network: #{envs['VLAN_INTERFACE']}")
      Log.info("docker network create -d macvlan -o parent=#{envs['INTERFACE']} \
        --subnet #{envs['HOST_SEGMENT']} \
        --gateway #{envs['GATEWAY_IP']} \
        --ip-range #{envs['VLAN_SEGMENT']} \
        --aux-address 'bridge=#{envs['VLAN_BRIDGE_IP']}' \
        --aux-address 'bridge=#{envs['VLAN_PREFIX']}' \
        #{envs['VLAN_INTERFACE']}")
      %x(docker network create -d macvlan -o parent=#{envs['INTERFACE']} \
        --subnet #{envs['HOST_SEGMENT']} \
        --gateway #{envs['GATEWAY_IP']} \
        --ip-range #{envs['VLAN_SEGMENT']} \
        --aux-address 'bridge=#{envs['VLAN_BRIDGE_IP']}' \
        --aux-address 'bridge=#{envs['VLAN_PREFIX']}' \
        #{envs['VLAN_INTERFACE']}
      )
      error_code = $?.exitstatus
      if error_code != 0
        raise NetworkError.new("Create docker macvlan network failed error_code: #{error_code}")
      end
    end

    # Remove macvlan network
    # Remove macvlan network bridge & set it launch at login
    def clean_network(envs)
      Log.info("Delete network bridge launch at login")
      print "Delete macblan network bridge launch at login need root privillege: "
      %x(sudo sh -c '(crontab -l ; grep -v anas_service.sh) | crontab - ')
      Log.info("Delete network bridge, execute: sudo sh #{rendered_file} del")
      %x("sudo sh #{rendered_file} del")
      Log.info("Remove docker macvlan network: #{envs['VLAN_INTERFACE']}")
      %x(docker network rm #{envs['VLAN_INTERFACE']})
    end

    # if don't specify base path, use default path -  ~/.anas
    # 
    # @return String, the base path
    def check_base_path(options_base)
      base_path = File.join(Dir.home, ".anas")
      if options_base
        base_path = Pathname.new(Dir.pwd) + options_base
        if base_path.file?
          raise PermissionError.new("#{base_path} is a file name, need a directory")
        end
        if base_path.directory?
          unless base_path.writable?
            raise PermissionError.new("User don't have permission to writable in #{base_path}")
          end
        else
          unless base_path.parent.writable?
            raise PermissionError.new("User don't have permission to writable in parent dir #{base_path.parent}")
          end
        end
      end
      return base_path
    end

    # Can perform 'build', 'start', 'stop', 'restart' action
    # Default base path is ~/.anas
    # Default config is ./config.yml
    # 'start' can run 'build' action first
    # 'build', must specify config.yml, working at [base]/tmp, after success, will copy to [base]/release
    # 'start', if only run `start` or not specify config.yml, will working at [base]/release,
    #          if run `build` first or specify config.yml, will working at [base]/tmp &
    #          before perform start action, it will run stop action in [base]/relase first.
    # 'restart', it will ignore the config.yml, working at [base]/relase,
    # 'stop', it will ignore the config.yml, working at [base]/relase,
    def run!
      options = @options
      actions = @actions
      base_path = check_base_path options['base']
      @base_path = base_path
      working_path = nil
      config = nil
      is_tmp_path = nil
      tmp_path = File.join(base_path, "tmp")
      release_path = File.join(base_path, "release")

      if actions.include? 'build'
        working_path = tmp_path
        path = options[:config]
        path || path = './config.yml'
        config = load_config(path)
        is_tmp_path = true
      elsif actions.include? 'start' && options[:config]
        working_path = tmp_path
        config = load_config(options[:config])
        is_tmp_path = true
      else 
        working_path = release_path
        config = load_config(File.join(working_path, 'config.yml'))
        is_tmp_path = false
      end

      if is_tmp_path
        Log.info("rm tmp #{tmp_path}")
        clean_tmp!(File.join(base_path, "tmp"))
      end

      Log.info("working_path is #{working_path}")
      mods = config['mods']
      Log.info("Config mods #{mods}")
      config_envs = config['envs']
      Log.info("Config envs #{config_envs}")

      Log.debug("Build DependentTree")
      dependent_tree = DependentTree.new(mods, config_envs)
      @dependent_tree = dependent_tree

      static_envs = get_static_envs(mods, config_envs)
      dependent_tree.set_working_path(working_path)

      envs = process_envs(mods, static_envs)

      if actions.include? 'build'
        build_mods(mods, envs)
      end

      if actions.include? 'start'
        # stop the old config first
        if is_tmp_path && File.directory?(release_path)
          starter = Anas::Starter.new(['stop'], options)
          starter.run!
        end
        start_mods(mods, envs)
      elsif actions.include? 'restart'
        stop_mods([dependent_tree.root_node.mod_name])
        start_mods(mods, envs)
      elsif actions.include? 'stop'
        stop_mods([dependent_tree.root_node.mod_name])
      end
      if is_tmp_path
        write_config!(config, File.join(working_path, 'config.yml'))
        sync_tmp!(File.join(base_path, "tmp"), release_path)
      end
    end

  end
end
