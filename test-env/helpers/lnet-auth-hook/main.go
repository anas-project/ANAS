// lnet-auth-hook is the Hook of lnauth, the synthetic forward_auth provider of
// the Incus lease network acceptance VM. It is never part of an ANAS release.
// Like a real provider it publishes the provider-qualified middleware its
// Compose labels define; the gate itself is lease-net-agent's --auth mode.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	var request struct {
		ABI   string `json:"abi"`
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		fail(err)
	}
	if request.ABI != "anas.module-hook/v1" {
		fail(fmt.Errorf("unsupported ABI %q", request.ABI))
	}
	env := map[string]string{}
	if request.Phase == "calculate" {
		env["ANAS_FORWARD_AUTH_MIDDLEWARE"] = "lnet-auth@docker"
		env["ANAS_FORWARD_AUTH_PROVIDER"] = "lnauth"
	}
	out, err := json.Marshal(map[string]any{"env": env, "secrets": map[string]string{}})
	if err != nil {
		fail(err)
	}
	fmt.Print(string(out))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
