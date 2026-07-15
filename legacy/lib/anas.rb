# $LOAD_PATH << '../casks/mods'
# lib = File.expand_path('../lib', __FILE__)
require 'anas/version'
require 'anas/commands'
require 'anas/base_runner'
require 'anas/utils'
require 'anas/helper'

module Anas
  class Error < StandardError; end
end
