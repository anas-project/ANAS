#!/usr/bin/env ruby

# require 'rubygems'
require 'commander'
require 'yaml'

module Anas
  class Commands
    include Commander::Methods
    # include whatever modules you need

    def self.start
      self.new.run
    end

    def run
      program :name, 'anas'
      program :version, Anas::VERSION
      program :description, 'TODO'
      program :help, 'Author', 'Hailong Wang <whlsxl+g@gmail.com>'
      program :help, 'GitHub', 'TODO'

      global_option('-b', '--base FILE', 'The ANAS base working path, storage docker compose, version lock & config file, default: ~/.anas')
      global_option('-y', 'Agree all default action in ANAS')
      global_option('--verbose')

      command :start do |c|
        c.syntax = 'anas start [options]'
        c.summary = ''
        c.description = ''
        c.example 'description', 'Start modules'
        c.option '--build', 'Build before start'
        c.option '-c', '--config FILE', 'Load config yml, default: ./config.yml'
        c.action do |args, options|
          options.default :build => false
          actions = ['start']
          options_new = options.__hash__
          if options_new[:build]
            actions.append('build')
          end
          if options_new[:verbose] == true then
            options_new[:log_level] = Logger::DEBUG
          else 
            options_new[:log_level] = Logger::WARN
          end
          starter = Anas::Starter.new(actions, options_new)
          starter.run!
        end
      end

      command :restart do |c|
        c.syntax = 'anas retart [options]'
        c.summary = ''
        c.description = ''
        c.example 'description', 'Restart modules'
        c.action do |args, options|
          options.default :build => false
          actions = ['restart']
          options_new = options.__hash__
          if options_new[:verbose] == true then
            options_new[:log_level] = Logger::DEBUG
          else 
            options_new[:log_level] = Logger::WARN
          end
          # unless options_new[:file].nil?
          #   config = load_config_file(options_new[:file])
          # end

          starter = Anas::Starter.new(actions, options_new)
          starter.run!
        end
      end

      command :build do |c|
        c.syntax = 'anas build [options]'
        c.summary = ''
        c.description = 'Build all modules'
        c.example 'description', 'Build all modules'
        c.option '-c', '--config FILE', 'Load config yml, default: ./config.yml'
        c.action do |args, options|
          # options.default :config => 'config.yml'
          options_new = options.__hash__
          actions = ['build']
          if options_new[:verbose] == true then
            options_new[:log_level] = Logger::DEBUG
          else 
            options_new[:log_level] = Logger::WARN
          end
          starter = Anas::Starter.new(actions, options_new)
          starter.run!
        end
      end

      command :stop do |c|
        c.syntax = 'anas stop [options]'
        c.summary = ''
        c.description = 'Stop modules'
        c.example 'description', 'Stop modules'
        c.option '-a', '--all', 'Stop all modules'
        c.action do |args, options|
          options_new = options.__hash__
          actions = ['stop']
          if options_new[:verbose] == true then
            options_new[:log_level] = Logger::DEBUG
          else 
            options_new[:log_level] = Logger::WARN
          end
          # unless options_new[:file].nil?
          #   config = load_config_file(options_new[:file])
          # end

          starter = Anas::Starter.new(actions, options_new)
          starter.run!
        end
      end

      # command :nn do |c|
      #   c.syntax = 'anas nn [options]'
      #   c.summary = ''
      #   c.description = ''
      #   c.example 'description', 'command example'
      #   c.option '--some-switch', 'Some switch that does something'
      #   c.action do |args, options|
      #     # Do something or c.when_called Anas::Commands::Nn
      #   end
      # end

      run!
    end

  end
end
# Anas.new.run if $0 == __FILE__
