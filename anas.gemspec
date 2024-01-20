require_relative 'lib/anas/version'

Gem::Specification.new do |spec|
  spec.name          = "anas"
  spec.version       = Anas::VERSION
  spec.authors       = ["Hailong Wang"]
  spec.email         = ["whlsxl+g@gmail.com"]

  spec.summary       = %q{run enterprise app by docker on nas}
  spec.description   = %q{Base on docker and docker-compose, run a group of enterprise apps, like nextcloud, gitlab.}
  spec.homepage      = "https://github.com"
  spec.required_ruby_version = Gem::Requirement.new(">= 2.3.0")

  spec.metadata["homepage_uri"] = spec.homepage
  # spec.metadata["source_code_uri"] = "https://github.com"
  # spec.metadata["changelog_uri"] = "TODO: Put your gem's CHANGELOG.md URL here."

  # Specify which files should be added to the gem when it is released.
  # The `git ls-files -z` loads the files in the RubyGem that have been added into git.
  spec.files         = Dir.chdir(File.expand_path('..', __FILE__)) do
    `git ls-files -z`.split("\x0").reject { |f| f.match(%r{^(test|spec|features)/}) }
  end
  spec.bindir        = "bin"
  spec.executables   = ["anas", "setup"]
  spec.require_paths = ["lib", 'casks/mods']

  spec.add_dependency 'commander', '~> 4.5'
  spec.add_dependency 'htauth', '~> 2.1'
  spec.add_dependency 'sshkey', '~> 2.0'
  spec.add_dependency 'total', '~> 0.4.1'

  # spec.add_development_dependency "bundler", '~> 2.0'
  # spec.add_development_dependency "rake", '~> 0'
end
