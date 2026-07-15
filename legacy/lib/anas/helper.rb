require 'ipaddr'
require 'highline/import'

class ::Hash
  # via https://stackoverflow.com/a/25835016/2257038
  def stringify_keys
    h = self.map do |k,v|
      v_str = if v.instance_of? Hash
                v.stringify_keys
              else
                v
              end

      [k.to_s, v_str]
    end
    Hash[h]
  end

  # via https://stackoverflow.com/a/25835016/2257038
  def symbol_keys
    h = self.map do |k,v|
      v_sym = if v.instance_of? Hash
                v.symbol_keys
              else
                v
              end

      [k.to_sym, v_sym]
    end
    Hash[h]
  end

  def value_to_string!
    self.each { |k, v| self[k] = v.to_s }
  end
end

class Array
  def plus(element)
    self.push(element) unless self.include?(element)
    return self
  end

  def minus(element)
    self.delete(element)
    return self
  end
end

CHARS = ('0'..'9').to_a + ('A'..'Z').to_a + ('a'..'z').to_a
class String
  def camelize
    return self if self !~ /_/ && self =~ /[A-Z]+.*/
    split('_').map{|e| e.capitalize}.join
  end

  def classify
    Object.const_get(self)
  end

  def yesno?(default = false)
    a = ''
    s = default ? '[Y/n]' : '[y/N]'
    d = default ? 'y' : 'n'
    until %w[y n].include? a
      a = ask("#{self} #{s} ") { |yn| yn.limit = 1, yn.validate = /[yn]/i }
      a = d if a.length == 0
    end
    a.downcase == 'y'
  end

  def mod_class!
    require "#{self}/runner"
    mod = self.mod_class
    mod.init
    return mod
  end

  def mod_class
    return "Anas::#{self.camelize}Runner".classify
  end

  def init_mod_class
    "Anas::#{self.camelize}Runner".classify.init
  end

  def underscore
    self.gsub(/::/, '/').
    gsub(/([A-Z]+)([A-Z][a-z])/,'\1_\2').
    gsub(/([a-z\d])([A-Z])/,'\1_\2').
    tr("-", "_").
    downcase
  end

  def cmd_exist
    begin
      result = `#{self}`
      if $?.exitstatus != 0
        return false
      end
      return result
    rescue => exception
      return false
    end
  end

  def self.random_password(length=10)
    CHARS.sort_by { rand }.join[0...length]
  end

  def self.random(length=10)
    CHARS.sort_by { rand }.join[0...length]
  end
end

class IPAddr
  def ^(other)
    return self.clone.set(@addr ^ coerce_other(other).to_i)
  end

  # 192.168.1.0/255.255.255.0
  def full_ip_mask
    return "#{_to_string(@addr)}/#{_to_string(@mask_addr)}"
  end

  # 255.255.255.0
  def full_mask
    return _to_string(@mask_addr)
  end
end
