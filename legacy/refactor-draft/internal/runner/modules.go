package runner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Module struct {
	Name       string
	Defaults   map[string]string
	Required   []string
	Deps       []string
	RunAfter   []string
	UseLDAP    bool
	UseHostLAN string
	Calc       func(env map[string]string, workdir string) error
	ModuleEnv  func(env map[string]string, workdir string) error
	Services   func(env map[string]string, all []string) []string
}

func registry() map[string]Module {
	return map[string]Module{
		"core":     {Name: "core", Required: []string{"BASE_DOMAIN", "EMAIL", "DEFAULT_ROOT_PASSWORD"}, Calc: calcCore},
		"lego":     {Name: "lego", Required: []string{"DNS_PROVIDER"}, Defaults: map[string]string{"LEGO_DNS_SERVER": "223.5.5.5"}, Calc: calcLego},
		"traefik":  {Name: "traefik", Deps: []string{"lego"}, Defaults: map[string]string{"TRAEFIK_BASE_PORT": "9000", "TRAEFIK_DOMAIN_PREFIX": "traefik"}, Calc: domainCalc("TRAEFIK", "traefik")},
		"bind":     {Name: "bind", Deps: []string{"core"}, Defaults: map[string]string{"BIND_DEBUG": "true"}, Calc: calcBind, ModuleEnv: krb5Env},
		"samba_dc": {Name: "samba_dc", Deps: []string{"lego", "bind"}, Defaults: map[string]string{"SAMBA_DC_APP_FILTER": "false", "SAMBA_DC_CREATE_STRUCTURE": "true", "SAMBA_DC_ADMIN_NAME": "admin", "SAMBA_DC_TEMPLATE_SHELL": "/bin/false", "SAMBA_DC_TEMPLATE_HOMEDIR": "/home/%D/%U", "SAMBA_DC_DOMAIN_USERS_GID_NUMBER": "10000", "SAMBA_DC_USER_COMPLEX_PASS": "true", "SAMBA_DC_USER_MAX_PASS_AGE": "70", "SAMBA_DC_USER_MIN_PASS_LENGTH": "7", "SAMBA_DC_LOG_LEVEL": "1"}, Calc: calcSambaDC, ModuleEnv: krb5Env},
		"samba_fs": {Name: "samba_fs", Defaults: map[string]string{"SAMBA_FS_LOG_LEVEL": "1", "SAMBA_FS_WSDD_LOG_LEVEL": "0", "SAMBA_FS_HOSTNAME": "SambaFS"}, RunAfter: []string{"samba_dc"}, UseHostLAN: "required", Calc: calcSambaFS},
		"postgres": {Name: "postgres", Defaults: map[string]string{"POSTGRES_USERNAME": "postgres", "POSTGRES_ADMINER_ENABLED": "false"}, Calc: calcPostgres, ModuleEnv: func(e map[string]string, _ string) error {
			e["POSTGRES_HOST_AUTH_METHOD"] = "trust"
			e["ADMINER_DESIGN"] = "nette"
			return nil
		}, Services: adminerServices("POSTGRES_ADMINER_ENABLED", "postgres_adminer")},
		"mariadb": {Name: "mariadb", Defaults: map[string]string{"MARIADB_ADMINER_ENABLED": "false"}, Calc: calcMariaDB, ModuleEnv: func(e map[string]string, _ string) error {
			e["ADMINER_DESIGN"] = "nette"
			e["MYSQL_ROOT_PASSWORD"] = e["MARIADB_ROOT_PASSWORD"]
			return nil
		}, Services: adminerServices("MARIADB_ADMINER_ENABLED", "mariadb_adminer")},
		"eturnal": {Name: "eturnal", Defaults: map[string]string{"TURN_PORT": "3478", "TURN_DOMAIN_PREFIX": "turn"}, Calc: calcEturnal, ModuleEnv: func(e map[string]string, _ string) error {
			e["TURN_RELAY_MIN_PORT"] = "50000"
			e["TURN_RELAY_MAX_PORT"] = "51000"
			return nil
		}},
		"nextcloud": {Name: "nextcloud", Deps: []string{"traefik", "eturnal"}, Defaults: map[string]string{"NEXTCLOUD_DOMAIN_PREFIX": "nc", "NEXTCLOUD_DB_NAME": "nextcloud", "NEXTCLOUD_PHONE_REGION": "CN", "NEXTCLOUD_RM_SKELETON_FILES": "false", "NEXTCLOUD_LOG_LEVEL": "2", "NEXTCLOUD_MEMORY_LIMIT": "1G", "NEXTCLOUD_UPLOAD_MAX_SIZE": "16G", "NEXTCLOUD_DEBUG": "false", "NEXTCLOUD_TALK_ENABLED": "true", "NEXTCLOUD_MEMORIES_ENABLED": "true"}, RunAfter: []string{"samba_dc", "postgres", "mariadb"}, UseLDAP: true, Calc: calcNextcloud, ModuleEnv: moduleNextcloud, Services: func(e map[string]string, all []string) []string {
			if e["NEXTCLOUD_TALK_ENABLED"] == "true" {
				return all
			}
			return remove(all, "anas_talk", "talk")
		}},
		"collabora":    {Name: "collabora", Deps: []string{"traefik"}, Defaults: map[string]string{"COLLABORA_DOMAIN_PREFIX": "collabora", "COLLABORA_LOG_LEVEL": "warning", "COLLABORA_INTERFACE": "default", "COLLABORA_AUTO_SAVE": "60"}, Calc: domainCalc("COLLABORA", "collabora"), ModuleEnv: moduleCollabora},
		"lam":          {Name: "lam", Deps: []string{"traefik"}, Defaults: map[string]string{"LAM_DOMAIN_PREFIX": "lam"}, RunAfter: []string{"samba_dc"}, Calc: calcLAM},
		"phpldapadmin": {Name: "phpldapadmin", Deps: []string{"traefik"}, Defaults: map[string]string{"PHPLDAPADMIN_DOMAIN_PREFIX": "phpldapadmin"}, RunAfter: []string{"samba_dc"}, Calc: domainCalc("PHPLDAPADMIN", "phpldapadmin")},
		"meshcentral":  {Name: "meshcentral", Deps: []string{"traefik", "mariadb"}, Defaults: map[string]string{"MESHCENTRAL_DOMAIN_PREFIX": "meshcentral", "MESHCENTRAL_MPS_PORT": "4433"}, RunAfter: []string{"samba_dc"}, UseLDAP: true, Calc: calcMeshcentral},
		"ddns":         {Name: "ddns", Deps: []string{"traefik"}, Required: []string{"DNS_PROVIDER"}, Defaults: map[string]string{"DDNS_DOMAIN_PREFIX": "ddns"}, Calc: calcDDNS},
		"llng":         {Name: "llng", Defaults: map[string]string{"LLNG_DOMAIN_PREFIX": "auth", "LLNG_MANAGER_DOMAIN_PREFIX": "auth-manager", "LLNG_TEST_DOMAIN_PREFIX": "auth-test", "LLNG_LOG_LEVEL": "warn", "LLNG_DB_NAME": "lemonldap-ng", "LLNG_ENABLE_TEST": "true", "LLNG_ADMINER_ENABLED": "false"}, RunAfter: []string{"traefik"}, Calc: calcLLNG, ModuleEnv: moduleLLNG, Services: adminerServices("LLNG_ADMINER_ENABLED", "LLNG_adminer")},
		"keycloak":     {Name: "keycloak", Defaults: map[string]string{"KEYCLOAK_DOMAIN_PREFIX": "auth", "KEYCLOAK_MANAGER_DOMAIN_PREFIX": "auth-manager", "KEYCLOAK_TEST_DOMAIN_PREFIX": "auth-test", "KEYCLOAK_LOG_LEVEL": "warn", "KEYCLOAK_DB_NAME": "lemonldap-ng", "KEYCLOAK_ENABLE_TEST": "true", "KEYCLOAK_ADMINER_ENABLED": "false"}, RunAfter: []string{"traefik"}, Calc: calcKeycloak, ModuleEnv: moduleKeycloak, Services: adminerServices("KEYCLOAK_ADMINER_ENABLED", "KEYCLOAK_adminer")},
		"netbird":      {Name: "netbird", Defaults: map[string]string{"NETBIRD_DOMAIN_PREFIX": "netbird", "NETBIRD_ADMINER_ENABLED": "false"}, Calc: calcNetbird, ModuleEnv: moduleNetbird, Services: adminerServices("NETBIRD_ADMINER_ENABLED", "NETBIRD_adminer")},
		"freeradius":   {Name: "freeradius", Deps: []string{"lego"}},
	}
}

func calcCore(e map[string]string, _ string) error {
	if e["SERVER_NAME"] == "" {
		if h, err := os.Hostname(); err == nil {
			e["SERVER_NAME"] = strings.ToUpper(strings.Split(h, ".")[0])
		}
	}
	if e["DEFAULT_SERVICE_ROOT_PASSWORD"] == "" {
		e["DEFAULT_SERVICE_ROOT_PASSWORD"] = e["DEFAULT_ROOT_PASSWORD"]
	}
	if e["HOST_IP"] == "" {
		if ip := firstIPv4(); ip != "" {
			e["HOST_IP"] = ip
		}
	}
	e["LOCAL_DNS_SERVER"] = e["HOST_IP"]
	e["USERDATA_PATH"] = defaultValue(e["USERDATA_PATH"], filepath.Join(e["DATA_PATH"], e["USERDATA_NAME"]))
	e["DOWNLOAD_DIR_NAME"] = defaultValue(e["DOWNLOAD_DIR_NAME"], "Downloads")
	e["MUSIC_DIR_NAME"] = defaultValue(e["MUSIC_DIR_NAME"], "Music")
	e["VIDEO_DIR_NAME"] = defaultValue(e["VIDEO_DIR_NAME"], "Video")
	if e["HOST_DNS_SERVER"] == "" {
		e["HOST_DNS_SERVER"] = e["DNS_SERVER"]
	}
	return nil
}

func calcLego(e map[string]string, _ string) error {
	e["LEGO_EMAIL"] = defaultValue(e["LEGO_EMAIL"], e["EMAIL"])
	e["LEGO_DATA_PATH"] = defaultValue(e["LEGO_DATA_PATH"], filepath.Join(e["DATA_PATH"], "lego/certs"))
	e["LEGO_CERTS_PATH"] = filepath.Join(e["LEGO_DATA_PATH"], "certificates") + string(os.PathSeparator)
	e["LEGO_CERTS_USER1000_PATH"] = filepath.Join(e["LEGO_DATA_PATH"], "certs1000") + string(os.PathSeparator)
	e["LEGO_CERT_NAME"] = e["BASE_DOMAIN"] + ".crt"
	e["LEGO_KEY_NAME"] = e["BASE_DOMAIN"] + ".key"
	e["LEGO_CA_CERT_NAME"] = e["BASE_DOMAIN"] + ".issuer.crt"
	return nil
}

func calcBind(e map[string]string, _ string) error {
	if e["BIND_DNS_FORWARDER"] == "" {
		e["BIND_DNS_FORWARDER"] = strings.ReplaceAll(e["HOST_DNS_SERVER"], " ", ";") + ";"
	}
	e["BIND_HOST_IP"] = e["HOST_IP"]
	return nil
}

func calcSambaDC(e map[string]string, _ string) error {
	domain := e["BASE_DOMAIN"]
	e["SAMBA_DC_DOMAIN"] = domain
	e["SAMBA_DC_DNS_SEARCH"] = domain
	e["SAMBA_DC_REALM"] = defaultValue(e["SAMBA_DC_REALM"], strings.ToUpper(domain))
	e["SAMBA_DC_NETBIOS_NAME"] = strings.ToUpper(defaultValue(e["SAMBA_DC_NETBIOS_NAME"], e["SERVER_NAME"]))
	e["SAMBA_DC_DC_NAME"] = strings.ToLower(e["SAMBA_DC_NETBIOS_NAME"])
	e["SAMBA_DC_DC_DOMAIN"] = e["SAMBA_DC_DC_NAME"] + "." + domain
	e["SAMBA_DC_ADMINISTRATOR_NAME"] = "Administrator"
	e["SAMBA_DC_ADMIN_DISPLAY_NAME"] = "Administrator"
	e["SAMBA_DC_ADMIN_PASSWORD"] = defaultValue(e["SAMBA_DC_ADMIN_PASSWORD"], e["DEFAULT_ROOT_PASSWORD"])
	e["SAMBA_DC_ADMINISTRATOR_PASSWORD"] = defaultValue(e["SAMBA_DC_ADMINISTRATOR_PASSWORD"], e["DEFAULT_ROOT_PASSWORD"])
	e["SAMBA_DC_LDAPS_SERVER_URL"] = defaultValue(e["SAMBA_DC_LDAPS_SERVER_URL"], "ldaps://"+domain)
	e["SAMBA_DC_HOST"] = domain
	e["SAMBA_DC_LDAPS_PORT"] = "636"
	e["SAMBA_DC_LDAPS_SERVER_URL_PORT"] = e["SAMBA_DC_LDAPS_SERVER_URL"] + ":" + e["SAMBA_DC_LDAPS_PORT"]
	e["SAMBA_DC_WORKGROUP"] = strings.ToUpper(defaultValue(e["SAMBA_DC_WORKGROUP"], strings.Split(domain, ".")[0]))
	baseDN := "DC=" + strings.Join(strings.Split(domain, "."), ",DC=")
	e["SAMBA_DC_BASE_DN"] = baseDN
	e["SAMBA_DC_BASE_COMPUTERS_DN"] = "CN=Computers," + baseDN
	e["SAMBA_DC_BASE_GROUPS_DN"] = "OU=Groups," + baseDN
	e["SAMBA_DC_BASE_GROUPS_ROLE_DN"] = "OU=Role," + e["SAMBA_DC_BASE_GROUPS_DN"]
	e["SAMBA_DC_BASE_USERS_DN_NAME"] = "People"
	e["SAMBA_DC_BASE_USERS_DN_PREFIX"] = "OU=" + e["SAMBA_DC_BASE_USERS_DN_NAME"]
	e["SAMBA_DC_BASE_USERS_DN"] = e["SAMBA_DC_BASE_USERS_DN_PREFIX"] + "," + baseDN
	e["SAMBA_DC_BASE_APP_DN"] = "OU=Apps," + e["SAMBA_DC_BASE_GROUPS_DN"]
	e["SAMBA_DC_APP_ALL_NAME"] = "APP_all"
	e["SAMBA_DC_APP_ALL_DN"] = "CN=" + e["SAMBA_DC_APP_ALL_NAME"] + "," + e["SAMBA_DC_BASE_APP_DN"]
	e["SAMBA_DC_ADMINISTRATOR_DN"] = "CN=Administrator,CN=Users," + baseDN
	e["SAMBA_DC_ADMIN_DN"] = "CN=" + e["SAMBA_DC_ADMIN_NAME"] + "," + e["SAMBA_DC_BASE_USERS_DN"]
	e["SAMBA_DC_ADMIN_GROUP_NAME"] = "Admins"
	e["SAMBA_DC_ADMIN_GROUP_DN"] = "CN=Admins," + e["SAMBA_DC_BASE_GROUPS_ROLE_DN"]
	e["SAMBA_DC_GROUP_CLASS_NAME"] = "group"
	e["SAMBA_DC_GROUP_CLASS_FILTER"] = "(objectClass=group)"
	e["SAMBA_DC_USER_CLASS_NAME"] = "user"
	e["SAMBA_DC_USER_CLASS_FILTER"] = "(objectClass=user)"
	e["SAMBA_DC_USER_ENABLED_FILTER"] = "(!(userAccountControl:1.2.840.113556.1.4.803:=2))"
	e["SAMBA_DC_USER_LOGIN_ATTRS"] = defaultValue(e["SAMBA_DC_USER_LOGIN_ATTRS"], "sAMAccountName,userPrincipalName,mail")
	e["SAMBA_DC_USER_NAME"] = "sAMAccountName"
	e["SAMBA_DC_USER_DISPLAY_NAME"] = "displayName"
	e["SAMBA_DC_GROUP_DISPLAY_NAME"] = "name"
	e["SAMBA_DC_GROUP_MEMBER_ATTR"] = "member"
	e["SAMBA_DC_USER_EMAIL"] = "mail"
	e["SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN"] = defaultValue(e["SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN"], domain)
	e["SAMBA_DC_INTERFACES"] = defaultValue(e["SAMBA_DC_INTERFACES"], e["INTERFACE"])
	return nil
}

func calcSambaFS(e map[string]string, _ string) error {
	e["SAMBA_FS_USE_DEFAULT_DOMAIN"] = defaultValue(e["SAMBA_FS_USE_DEFAULT_DOMAIN"], e["USE_DEFAULT_DOMAIN"])
	e["SAMBA_FS_NETBIOS_NAME"] = strings.ToUpper(defaultValue(e["SAMBA_FS_NETBIOS_NAME"], e["SAMBA_FS_HOSTNAME"]))
	return nil
}

func calcPostgres(e map[string]string, _ string) error {
	e["POSTGRES_NETWORK_NAME"] = defaultValue(e["POSTGRES_NETWORK_NAME"], e["NETWORK_PREFIX"]+"postgres")
	e["POSTGRES_PASSWORD"] = defaultValue(e["POSTGRES_PASSWORD"], e["DEFAULT_SERVICE_ROOT_PASSWORD"])
	e["POSTGRES_USER"] = e["POSTGRES_USERNAME"]
	e["POSTGRES_HOST"] = e["CONTAINER_PREFIX"] + "postgres"
	e["POSTGRES_PORT"] = "5432"
	e["POSTGRES_HOST_PORT"] = e["POSTGRES_HOST"] + ":" + e["POSTGRES_PORT"]
	e["POSTGRES_ADMINER_DOMAIN_PREFIX"] = defaultValue(e["POSTGRES_ADMINER_DOMAIN_PREFIX"], "postgres_adminer")
	e["POSTGRES_ADMINER_DOMAIN"] = e["POSTGRES_ADMINER_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	return nil
}

func calcMariaDB(e map[string]string, _ string) error {
	e["MARIADB_NETWORK_NAME"] = defaultValue(e["MARIADB_NETWORK_NAME"], e["NETWORK_PREFIX"]+"mariadb")
	e["MARIADB_ROOT_PASSWORD"] = defaultValue(e["MARIADB_ROOT_PASSWORD"], e["DEFAULT_SERVICE_ROOT_PASSWORD"])
	e["MARIADB_PASSWORD"] = e["MARIADB_ROOT_PASSWORD"]
	e["MARIADB_USERNAME"] = "root"
	e["MARIADB_HOST"] = e["CONTAINER_PREFIX"] + "mariadb"
	e["MARIADB_PORT"] = "3306"
	e["MARIADB_HOST_PORT"] = e["MARIADB_HOST"] + ":3306"
	e["MYSQL_HOST"] = e["MARIADB_HOST"]
	e["MYSQL_PORT"] = e["MARIADB_PORT"]
	e["MYSQL_USERNAME"] = e["MARIADB_USERNAME"]
	e["MYSQL_PASSWORD"] = e["MARIADB_PASSWORD"]
	e["MARIADB_ADMINER_DOMAIN_PREFIX"] = defaultValue(e["MARIADB_ADMINER_DOMAIN_PREFIX"], "mariadb_adminer")
	e["MARIADB_ADMINER_DOMAIN"] = e["MARIADB_ADMINER_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	return nil
}

func calcEturnal(e map[string]string, _ string) error {
	e["TURN_HOSTNAME"] = e["CONTAINER_PREFIX"] + "eturnal"
	e["TURN_SECRET"] = defaultValue(e["TURN_SECRET"], randomHex(16))
	e["TURN_DOMAIN"] = defaultValue(e["TURN_DOMAIN"], e["TURN_DOMAIN_PREFIX"]+"."+e["BASE_DOMAIN"])
	e["TURN_DOMAIN_PORT"] = e["TURN_DOMAIN"] + ":" + e["TURN_PORT"]
	return nil
}

func calcNextcloud(e map[string]string, _ string) error {
	e["NEXTCLOUD_HOSTNAME"] = e["CONTAINER_PREFIX"] + "nextcloud"
	e["NEXTCLOUD_BASE_PATH"] = defaultValue(e["NEXTCLOUD_BASE_PATH"], filepath.Join(e["DATA_PATH"], "nextcloud"))
	e["NEXTCLOUD_DOMAIN"] = e["NEXTCLOUD_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["NEXTCLOUD_DOMAIN_PORT"] = e["NEXTCLOUD_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["NEXTCLOUD_DOMAIN_FULL"] = "https://" + e["NEXTCLOUD_DOMAIN_PORT"]
	e["NEXTCLOUD_TALK_SIGNALING_DOMAIN_FULL"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/talk"
	e["NEXTCLOUD_REDIS_HOSTNAME"] = e["CONTAINER_PREFIX"] + "nextcloud_redis"
	e["NEXTCLOUD_REDIS_PORT"] = "6379"
	if e["NEXTCLOUD_DB_TYPE"] == "" {
		if e["POSTGRES_HOST"] != "" {
			e["NEXTCLOUD_DB_TYPE"] = "postgres"
		} else if e["MARIADB_HOST"] != "" {
			e["NEXTCLOUD_DB_TYPE"] = "mariadb"
		}
	}
	if e["NEXTCLOUD_DB_TYPE"] == "mariadb" {
		e["NEXTCLOUD_NETWORK_DB"] = e["MARIADB_NETWORK_NAME"]
	} else {
		e["NEXTCLOUD_NETWORK_DB"] = e["POSTGRES_NETWORK_NAME"]
	}
	e["NEXTCLOUD_IMAGINARY_HOSTNAME"] = e["CONTAINER_PREFIX"] + "imaginary"
	e["NEXTCLOUD_ADMIN_USERNAME"] = defaultValue(e["NEXTCLOUD_ADMIN_USERNAME"], e["SAMBA_DC_ADMIN_NAME"]+"_nc")
	e["NEXTCLOUD_ADMIN_PASSWORD"] = defaultValue(e["NEXTCLOUD_ADMIN_PASSWORD"], e["SAMBA_DC_ADMIN_PASSWORD"])
	e["NEXTCLOUD_TALK_INTERNAL_SECRET"] = defaultValue(e["NEXTCLOUD_TALK_INTERNAL_SECRET"], randomHex(16))
	e["TALK_SIGNALING_SECRET"] = defaultValue(e["TALK_SIGNALING_SECRET"], randomHex(16))
	return nil
}

func moduleNextcloud(e map[string]string, _ string) error {
	e["MEMORY_LIMIT"] = e["NEXTCLOUD_MEMORY_LIMIT"]
	e["UPLOAD_MAX_SIZE"] = e["NEXTCLOUD_UPLOAD_MAX_SIZE"]
	e["OPCACHE_MEM_SIZE"] = "128"
	e["APC_SHM_SIZE"] = "128M"
	e["REAL_IP_HEADER"] = "X-Forwarded-For"
	e["LOG_IP_VAR"] = "http_x_forwarded_for"
	e["HSTS_HEADER"] = "max-age=15768000; includeSubDomains"
	e["RP_HEADER"] = "strict-origin"
	e["SUBDIR"] = ""
	if e["NEXTCLOUD_DB_TYPE"] == "postgres" {
		e["DB_HOST"] = e["POSTGRES_HOST_PORT"]
		e["DB_USER"] = e["POSTGRES_USERNAME"]
		e["DB_PASSWORD"] = e["POSTGRES_PASSWORD"]
		e["DB_TYPE"] = "pgsql"
	} else {
		e["DB_HOST"] = e["MARIADB_HOST_PORT"]
		e["DB_USER"] = e["MARIADB_USERNAME"]
		e["DB_PASSWORD"] = e["MARIADB_PASSWORD"]
		e["DB_TYPE"] = "mysql"
	}
	e["DB_NAME"] = e["NEXTCLOUD_DB_NAME"]
	return nil
}

func domainCalc(prefix, service string) func(map[string]string, string) error {
	return func(e map[string]string, _ string) error {
		e[prefix+"_HOSTNAME"] = e["CONTAINER_PREFIX"] + service
		e[prefix+"_DOMAIN"] = e[prefix+"_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
		e[prefix+"_DOMAIN_PORT"] = e[prefix+"_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
		e[prefix+"_DOMAIN_FULL"] = "https://" + e[prefix+"_DOMAIN_PORT"]
		return nil
	}
}

func calcLAM(e map[string]string, _ string) error {
	e["LAM_LANGUAGE"] = defaultValue(e["LAM_LANGUAGE"], e["DEFAULT_LANGUAGE"])
	e["LAM_DOMAIN"] = e["LAM_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["LAM_ADMIN_PASSWORD"] = defaultValue(e["LAM_ADMIN_PASSWORD"], e["SAMBA_DC_ADMIN_PASSWORD"])
	return nil
}

func calcMeshcentral(e map[string]string, _ string) error {
	e["MESHCENTRAL_DOMAIN"] = e["MESHCENTRAL_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["MESHCENTRAL_TITLE"] = defaultValue(e["MESHCENTRAL_TITLE"], e["SERVER_NAME"])
	e["MESHCENTRAL_SUBTITLE"] = defaultValue(e["MESHCENTRAL_SUBTITLE"], " ")
	return nil
}

func calcDDNS(e map[string]string, _ string) error {
	e["DDNS_DNS_SERVER"] = defaultValue(e["DDNS_DNS_SERVER"], e["DNS_SERVER"])
	e["DDNS_DOMAIN"] = e["DDNS_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	return nil
}

func calcLLNG(e map[string]string, _ string) error     { return identityCalc("LLNG")(e) }
func calcKeycloak(e map[string]string, _ string) error { return identityCalc("KEYCLOAK")(e) }

func identityCalc(prefix string) func(map[string]string) error {
	return func(e map[string]string) error {
		e[prefix+"_PASSWORD"] = defaultValue(e[prefix+"_PASSWORD"], e["DEFAULT_SERVICE_ROOT_PASSWORD"])
		for _, part := range []string{"", "_TEST", "_MANAGER"} {
			keyPrefix := prefix + part
			domainPrefixKey := keyPrefix + "_DOMAIN_PREFIX"
			if part == "" {
				domainPrefixKey = prefix + "_DOMAIN_PREFIX"
			}
			if e[domainPrefixKey] == "" {
				continue
			}
			e[keyPrefix+"_DOMAIN"] = e[domainPrefixKey] + "." + e["BASE_DOMAIN"]
			e[keyPrefix+"_DOMAIN_PORT"] = e[keyPrefix+"_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
			e[keyPrefix+"_DOMAIN_FULL"] = "https://" + e[keyPrefix+"_DOMAIN_PORT"]
		}
		if e[prefix+"_DB_TYPE"] == "" {
			if e["POSTGRES_HOST"] != "" {
				e[prefix+"_DB_TYPE"] = "postgres"
			} else if e["MARIADB_HOST"] != "" {
				e[prefix+"_DB_TYPE"] = "mariadb"
			}
		}
		if e[prefix+"_DB_TYPE"] == "mariadb" {
			e[prefix+"_NETWORK_DB"] = e["MARIADB_NETWORK_NAME"]
		} else {
			e[prefix+"_NETWORK_DB"] = e["POSTGRES_NETWORK_NAME"]
		}
		e[prefix+"_OIDC_CONFIGURATION_ENDPOINT"] = e[prefix+"_DOMAIN_FULL"] + "/.well-known/openid-configuration"
		return nil
	}
}

func moduleLLNG(e map[string]string, w string) error     { return moduleIdentity("LLNG", e, w) }
func moduleKeycloak(e map[string]string, w string) error { return moduleIdentity("KEYCLOAK", e, w) }
func moduleIdentity(prefix string, e map[string]string, _ string) error {
	if e[prefix+"_DB_TYPE"] == "postgres" {
		e["DB_HOST"] = e["POSTGRES_HOST"]
		e["DB_POST"] = e["POSTGRES_PORT"]
		e["DB_USER"] = e["POSTGRES_USERNAME"]
		e["DB_PASSWORD"] = e["POSTGRES_PASSWORD"]
	} else {
		e["DB_HOST"] = e["MARIADB_HOST"]
		e["DB_POST"] = e["MARIADB_PORT"]
		e["DB_USER"] = e["MARIADB_USERNAME"]
		e["DB_PASSWORD"] = e["MARIADB_PASSWORD"]
	}
	return nil
}

func calcNetbird(e map[string]string, _ string) error {
	e["NETBIRD_DOMAIN"] = e["NETBIRD_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["NETBIRD_DOMAIN_PORT"] = e["NETBIRD_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["NETBIRD_DOMAIN_FULL"] = "https://" + e["NETBIRD_DOMAIN_PORT"]
	e["OIDC_RP__NETBIRD__CLIENT_ID"] = "netbird"
	e["OIDC_RP__NETBIRD__CLIENT_SECRET"] = defaultValue(e["OIDC_RP__NETBIRD__CLIENT_SECRET"], randomHex(6))
	return nil
}

func moduleNetbird(e map[string]string, _ string) error {
	e["AUTH_AUDIENCE"] = "netbird"
	e["NETBIRD_DASHBOARD_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_MGMT_API_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_MGMT_GRPC_API_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_MGMT_API_PORT"] = e["TRAEFIK_BASE_PORT"]
	e["AUTH_CLIENT_ID"] = e["OIDC_RP__NETBIRD__CLIENT_ID"]
	e["AUTH_CLIENT_SECRET"] = e["OIDC_RP__NETBIRD__CLIENT_SECRET"]
	e["NETBIRD_SIGNAL_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_SIGNAL_PORT"] = e["TRAEFIK_BASE_PORT"]
	e["AUTH_REDIRECT_URI"] = "/auth"
	e["AUTH_SILENT_REDIRECT_URI"] = "/silent-auth"
	e["NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT"] = e["LLNG_OIDC_CONFIGURATION_ENDPOINT"]
	e["AUTH_SUPPORTED_SCOPES"] = "openid profile email"
	e["AUTH_DEVICE_AUTH_PROVIDER"] = "false"
	e["USE_AUTH0"] = "false"
	return nil
}

func moduleCollabora(e map[string]string, _ string) error {
	e["TIMEZONE"] = e["TZ"]
	e["CONTAINER_NAME"] = e["CONTAINER_PREFIX"] + "collabora"
	e["ADMIN_USER"] = e["SAMBA_DC_ADMIN_NAME"]
	e["ADMIN_PASS"] = e["DEFAULT_ROOT_PASSWORD"]
	e["ALLOWED_HOSTS"] = e["NEXTCLOUD_DOMAIN_FULL"]
	e["INTERFACE"] = e["COLLABORA_INTERFACE"]
	e["LOG_TYPE"] = "CONSOLE"
	e["LOG_LEVEL"] = e["COLLABORA_LOG_LEVEL"]
	e["ENABLE_TLS"] = "FALSE"
	e["ENABLE_TLS_CERT_GENERATE"] = "FALSE"
	e["ENABLE_TLS_REVERSE_PROXY"] = "TRUE"
	e["AUTO_SAVE"] = e["COLLABORA_AUTO_SAVE"]
	e["HOSTNAME"] = e["COLLABORA_DOMAIN_PORT"]
	e["FRAME_ANCESTORS"] = "https://*"
	e["ENABLE_CLEANUP"] = "true"
	return nil
}

func krb5Env(e map[string]string, _ string) error { e["KRB5RCACHETYPE"] = "none"; return nil }
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func randomHex(n int) string { b := make([]byte, n); _, _ = rand.Read(b); return hex.EncodeToString(b) }

func firstIPv4() string {
	out, err := exec.Command("sh", "-c", "ip -j -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.*\"prefsrc\":\"\\([^\"]*\\)\".*/\\1/p'").Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return strings.TrimSpace(string(out))
	}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ip := ipnet.IP.To4(); ip != nil {
					return ip.String()
				}
			}
		}
	}
	return ""
}

func adminerServices(flag, service string) func(map[string]string, []string) []string {
	return func(e map[string]string, all []string) []string {
		if e[flag] == "true" {
			return all
		}
		return remove(all, service, "anas_"+service)
	}
}

func remove(in []string, names ...string) []string {
	skip := map[string]bool{}
	for _, n := range names {
		skip[n] = true
	}
	out := []string{}
	for _, n := range in {
		if !skip[n] {
			out = append(out, n)
		}
	}
	return out
}

func requireKeys(e map[string]string, keys []string) error {
	missing := []string{}
	for _, k := range keys {
		if strings.TrimSpace(e[k]) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}
