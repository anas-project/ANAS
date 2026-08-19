package config

import (
	"fmt"
	"strings"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/topologyschema"
)

var topologyParameterTypes = map[string]configschema.Parameter{
	"identity.iam.default_protocol": {
		Kind: "enum",
		Enum: topologyschema.IAMProtocols(),
	},
	"modules.*.identity.login_protocol": {
		Kind: "enum",
		Enum: topologyschema.IAMLoginProtocols(),
	},
}

func normalizeTopologyParameters(cfg *File) error {
	defaultProtocol := cfg.Identity.IAM.DefaultProtocol
	if strings.TrimSpace(defaultProtocol) == "" {
		defaultProtocol = topologyschema.IAMProtocolOIDC
	}
	normalized, err := topologyParameterTypes["identity.iam.default_protocol"].Normalize(defaultProtocol)
	if err != nil {
		return fmt.Errorf("identity.iam.default_protocol: %w", err)
	}
	cfg.Identity.IAM.DefaultProtocol = normalized

	for name, module := range cfg.Modules.Values {
		normalized, err := topologyParameterTypes["modules.*.identity.login_protocol"].Normalize(module.Identity.LoginProtocol)
		if err != nil {
			return fmt.Errorf("modules.%s.identity.login_protocol: %w", name, err)
		}
		module.Identity.LoginProtocol = normalized
		cfg.Modules.Values[name] = module
	}
	return nil
}
