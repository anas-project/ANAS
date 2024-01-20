
module Anas

  # The base runner for every docker-compose module
  # Every runner should inherit BaseRunner to provide the basic functions
  class BaseRunner
    # @return [Array<String>] list of required envs
    def self.required_envs; @required_envs end

    # @return [Array<String>] optional envs is required by some functions
    def self.optional_envs; @optional_envs end

    # @return [Hash<String, String>] the default value of env
    def self.default_envs; @default_envs end

    # @return [String] the module name of child class
    attr_reader :mod_name

    # @return [String] the tmp dir path,
    #    working_path will be "${@base_path}/@mod_name"
    #    default is File.join(Dir.tmpdir, "anas")
    attr_reader :base_path
    
    # @return [String] copy & render erb files to temp dir
    attr_reader :working_path

    # @return [Hash<String, Any>] All the envs use in docker
    attr_accessor :envs

    # @return [String] use `docker compose` or `docker-compose`
    attr_accessor :docker_compose_cmd

    # @return [Runner] `core` runner
    attr_accessor :core_runner

    def base_path=(new_base_path)
      Log.debug("#{@mod_name} set base_path #{new_base_path}")
      @base_path = new_base_path
      @working_path = File.join(@base_path, @mod_name)
      # @prepare_path = File.join(@base_path, @mod_name + '.tmp')
      FileUtils.mkdir_p @working_path
      # FileUtils.mkdir_p @prepare_path
    end

    # Runner should set the values
    def initialize()
      @working_path = nil
      @is_gened_files = false
      @docker_compose_cmd = nil
      @mod_name = self.class.mod_name
      Log.info("Init `#{@mod_name}` class")
      # ObjectSpace.define_finalizer(self, lambda do |id| 
        # FileUtils.remove_entry(@working_path)
      # end)
    end

    def self.init
      # Runner should set the values
      @required_envs = []
      @default_envs = {}
      @optional_envs = []
    end

    def reset_temp_path
      FileUtils.rm_rf(@working_path)
      Log.info("reset #{@mod_name} working path #{@working_path} ")
    end

    # get the module name by class name
    # 
    # @return [String] the module name
    def self.mod_name
      class_name = self.name
      class_name = class_name.split('::').last
      class_name.slice!('Runner')
      return class_name.underscore
    end

    def envs=(new_envs)
      @envs = append_module_env(new_envs)
    end

    def docker_services_list
      result = %x(#{@docker_compose_cmd} config --services)
      return result.split("\n")
    end
    
    # return the services list base on @envs
    # 
    # @return String the list of services
    def docker_services_list_string
      return docker_services_list.join(' ')
    end

    # Get the docker-compose.yml file path
    # 
    # @return [String] the module path
    def get_docker_compose_path
      root_path = File.expand_path('../../..', __FILE__)
      return File.join(root_path, 'casks/mods', @mod_name)
    end
    
    # Copy all file to working path, render all erb files
    def render_files!(envs)
      Log.debug("Copy `#{@mod_name}` docker_compose dir from #{get_docker_compose_path} to #{@working_path}")
      FileUtils.cp_r("#{get_docker_compose_path}/.", @working_path)
      Log.info("Rendering `#{@mod_name}` erbs")
      envs_hash = {envs: envs}
      Dir.glob("#{@working_path}/**/*.erb").each do |erb_file|
        return if File.directory?(erb_file)
        Log.info("Rendering #{erb_file}")
        rendered = ERB.new(File.read(erb_file), nil).result_with_hash(envs_hash)
        Log.debug("Rendered content\n#{rendered}")
        FileUtils.remove_file(erb_file)
        Log.debug("Removed erb file #{erb_file}")
        rendered_file = File.expand_path(File.basename(erb_file, '.erb'), File.dirname(erb_file))
        Log.debug("Render content to #{rendered_file}")
        File.open(rendered_file, 'w') { |file| file.write(rendered) }
      end
    end

    # Check the required envs in calculated envs
    # 
    # @param envs [Array<String>] list of calculated envs
    # @return [Array<String>] missing envs
    public

    def check_envs(envs)
      Log.info("Checking `#{@mod_name}` envs")
      missing_envs = []
      self.class.required_envs.each do |env|
        Log.debug("Checking #{env}: #{envs[env] || ''}")
        unless envs.key?(env) && !envs[env].empty?
          missing_envs.append(env)
        end
      end
      return missing_envs
    end

    # After calculate default_envs & config_envs,
    # this is the last chance to change the envs
    # 
    # @param envs [Hash<String, String>] list of calculated envs
    # @return [Hash<String, String>] missing envs
    def cal_envs(envs)
      return envs
    end

    def module_envs(envs)
      return envs
    end

    # Copy file to container when container is up
    # 
    # @param container_name [String] container name
    # @param source [String] the source file path in host
    # @param des [String] the destination file path in container
    # @param exception [Boolean] raise exception or return result
    # @return [Boolean] copy success or not
    def cp_to_container(container_name, source, des, exception=true)
      return system("docker cp #{source} #{container_name}:#{des}", exception: exception)
    end

    # dependency must run before current
    # Attention: only config envs & current mods's default envs will be use, 
    # @param base_envs [Hash<String, String>] config_envs & mod's default_envs
    # @return [Array<String>] list of run before current
    def self.dependent_mods(base_envs)
      return ['core']
    end

    def append_module_env(envs)
      # Add module related env
      new_envs = module_envs(envs)
      envs_clone = new_envs.clone
      envs_clone.value_to_string!
      envs_clone['MODULE_NAME'] = @mod_name
      return envs_clone
    end

    def gen_files(envs)
      # envs = append_module_env(envs)
      gen_envs_file!(envs)
      render_files!(envs)
      @is_gened_files = true
    end

    def gen_envs_file!(envs)
      env_file = File.expand_path(".env", @working_path)
      Log.info("Gen envs file #{env_file}")
      File.open(env_file, 'w') do |file|
        envs.each do |key, value|
          file.write "#{key}='#{value}'\n"
        end
      end
    end

    def load_envs_file_from_working_path
      env_file = File.expand_path("#{@mod_name}.env", @working_path)
      Log.info("Load envs file #{env_file}")
      envs = {}
      File.open(env_file, 'r') do |file|
        file.each_line do |line|
          env = line.split('=')
          return if env.length == 0
          env[1] = "" if env[1].nil?
          envs[env[0]] = env[1]
        end
      end
      @envs = envs
    end

    def build
      Log.info("Building #{@mod_name}...")
      begin
        the_path = @working_path
        Log.debug("Entry @working_path #{the_path}")
        Dir.chdir(the_path)
        # result = system("#{@docker_compose_cmd} build", exception: true)
        slist = docker_services_list_string
        Log.info("Building services #{slist}")
        # ENV.update(@envs)
        Log.info("Exec `#{@docker_compose_cmd} build #{slist}`")
        system(@envs, "#{@docker_compose_cmd} build #{slist}", exception: true)
      rescue => exception
        Log.error("Building #{@mod_name} ERROR #{exception}")
        raise exception
      end
      Log.info("Module has been built #{@mod_name}")
    end

    def after_start_action(envs)
      Log.info("After start #{@mod_name}...")
    end

    def start
      Log.info("Starting #{@mod_name}...")
      begin
        the_path = @working_path
        Log.debug("Entry @working_path #{the_path}")
        Dir.chdir(the_path)
        # result = system("#{@docker_compose_cmd} up -d", exception: true)
        slist = docker_services_list_string
        Log.info("Starting services #{slist}")
        ENV.update(@envs)
        Log.info("Exec `#{@docker_compose_cmd} up #{slist} -d`")
        result = %x(#{@docker_compose_cmd} up #{slist} -d)
        after_start_action envs
      rescue => exception
        Log.error("Start #{@mod_name} ERROR #{exception}")
        raise exception
      end
      Log.info("Module started #{@mod_name}")
    end

    def restart
      begin
        stop
      rescue => exception
        Log.info("Stop ERROR, but doesn't matter")
      end
      start
    end

    def stop
      Log.info("Stoping #{@mod_name}...")
      begin
        Log.debug("Entry @working_path #{@working_path}")
        Dir.chdir(@working_path)
        # result = system("#{@docker_compose_cmd} down")
        ENV.update(@envs)
        Log.info("Exec `#{@docker_compose_cmd} down`")
        result = system("#{@docker_compose_cmd} down")
      rescue => exception
        Log.error("Stop #{@mod_name} ERROR #{exception}")
        raise exception
      end
      Log.info("Module stopped #{@mod_name}")
    end

    def use_ldap?
      return false
    end

    # mod don't have depend relationship, but need run after this mods
    # 
    # @param envs [Hash<String, String>] config_envs & mod's default_envs
    # @return [Array<String>] list of run after current
    def run_after_mods(envs)
      return []
    end

    # mod don't have depend relationship, but need run before this mods
    # 
    # @param envs [Hash<String, String>] config_envs & mod's default_envs
    # @return [Array<String>] list of run before current
    def run_before_mods(envs)
      return []
    end

    def is_running?
      Dir.chdir(@working_path)
      ENV.update(@envs)
      running = %x(#{@docker_compose_cmd} ps --services --filter "status=running")
      return running.empty?
    end

    def ensure_env!(envs, env_name)
      if envs[env_name].nil?
        Log.error("Missing envs #{env_name}")
        raise NoENVError.new(env_name)
      end
    end

    # Append new envs in .env file
    def append_envs(new_envs)
      env_file = File.expand_path(".env", @working_path)
      Log.info("Append envs file #{env_file}")
      File.open(env_file, 'a') do |file|
        new_envs.each do |key, value|
          file.write "#{key}=#{value}\n"
        end
      end
    end

    # Whether the mod need to use Host Lan.
    # 
    # @return [String] value can be 'required', 'optional' or nil
    def use_host_lan?
      return nil
    end

    # Mod neec domain
    # return domain Array, include a Hash like 
    # [
    #   ['inner', 'nc', 'nextcloud']
    # ]
    # [0]: type
    # [1]: domain_prefix
    # [2]: inner_domain
    # type: inner => host ip, dhcp => macvlan
    # @return [Array<Hash<String, String>>] 
    def domain(envs)
      key = "#{@mod_name.upcase}_DOMAIN_PREFIX"
      if envs.has_key?(key) && !envs[key].nil?
        return [['inner', envs[key], @mod_name]] # type, domain_prefix, inner_domain
      end
      return nil
    end
  end
end