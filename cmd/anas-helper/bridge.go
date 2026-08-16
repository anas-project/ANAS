package main

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// bridgeRequest is one validated bridge operation. Parsing and acting are
// separate so that every rule about what may be touched is decided before any
// privilege is used, and can be tested without any.
type bridgeRequest struct {
	Down    bool
	Name    string
	Parent  string
	Address string
	Routes  []string
}

// managedInterface is the whole of this program's blast radius.
//
// Every operation names the interface it acts on, and every name must match
// this. That is what makes "anas configures the network" different from "anas
// might take the machine off the network": the host's own interfaces, its
// addresses, its default route and its resolver are not addressable from here
// at all, whatever arguments arrive.
var managedInterface = regexp.MustCompile(`^anas[a-z0-9_]*$`)

// interfaceName is the kernel's own constraint (IFNAMSIZ, minus the
// terminator) plus a character set that cannot be confused for an option or a
// shell metacharacter. The parent is only ever referenced, never modified, so
// it is not required to be one of ours.
var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

func parseBridge(args []string) (bridgeRequest, error) {
	request := bridgeRequest{}
	switch args[0] {
	case "up":
	case "down":
		request.Down = true
	default:
		return request, fmt.Errorf("bridge %q is not up or down", args[0])
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		flag := rest[i]
		if !strings.HasPrefix(flag, "--") {
			return request, fmt.Errorf("unexpected argument %q", flag)
		}
		if i+1 >= len(rest) {
			return request, fmt.Errorf("%s needs a value", flag)
		}
		value := rest[i+1]
		i++
		switch flag {
		case "--name":
			request.Name = value
		case "--parent":
			request.Parent = value
		case "--address":
			request.Address = value
		case "--route":
			request.Routes = append(request.Routes, value)
		default:
			return request, fmt.Errorf("unknown flag %q", flag)
		}
	}
	return request, validateBridge(request)
}

func validateBridge(request bridgeRequest) error {
	if !managedInterface.MatchString(request.Name) {
		return fmt.Errorf("refusing to touch interface %q: this command only manages interfaces named anas*", request.Name)
	}
	if request.Down {
		if request.Parent != "" || request.Address != "" || len(request.Routes) > 0 {
			return fmt.Errorf("bridge down takes only --name")
		}
		return nil
	}
	if !interfaceName.MatchString(request.Parent) {
		return fmt.Errorf("--parent %q is not a valid interface name", request.Parent)
	}
	if request.Parent == request.Name {
		return fmt.Errorf("--parent and --name are both %q; a macvlan interface cannot be its own parent", request.Name)
	}
	if err := validateCIDR("--address", request.Address); err != nil {
		return err
	}
	for _, route := range request.Routes {
		if err := validateCIDR("--route", route); err != nil {
			return err
		}
	}
	return nil
}

// validateCIDR requires the prefix length to be written out. `ip addr add
// 192.168.1.50 dev x` is accepted by iproute2 and means /32, which for a
// bridge address silently produces an interface with no route to the
// containers behind it -- a failure that surfaces as "the host cannot reach
// the container" long after the command that caused it.
func validateCIDR(flag, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", flag)
	}
	if !strings.Contains(value, "/") {
		return fmt.Errorf("%s %q must include a prefix length", flag, value)
	}
	ip, _, err := net.ParseCIDR(value)
	if err != nil {
		return fmt.Errorf("%s %q is not a valid address: %w", flag, value, err)
	}
	if ip.To4() == nil {
		return fmt.Errorf("%s %q is not IPv4", flag, value)
	}
	return nil
}

// hostState is what the plan depends on that is not in the request: whether
// the interface is already there, and what addresses it already carries. Both
// are readable without any privilege, so they are gathered before anything is
// raised.
type hostState struct {
	InterfaceExists bool
	Addresses       []string
}

// bridgeCommands is the exact sequence of ip(8) invocations a request becomes.
//
// It returns them rather than running them so the whole plan can be asserted in
// a test: this is the code that runs with CAP_NET_ADMIN, and what it will do to
// a host should be readable without a host.
//
// Stale addresses are removed rather than left beside the new one. An address
// from an earlier plan keeps the host answering for a range this deployment no
// longer owns, and keeps a connected route pointing at the bridge -- and `ip
// addr replace` does not remove it, because a different prefix length makes it
// a different address as far as replace is concerned.
func bridgeCommands(request bridgeRequest, state hostState) [][]string {
	if request.Down {
		return [][]string{{"link", "delete", request.Name}}
	}
	commands := [][]string{}
	if !state.InterfaceExists {
		commands = append(commands, []string{
			"link", "add", request.Name, "link", request.Parent, "type", "macvlan", "mode", "bridge",
		})
	}
	commands = append(commands, []string{"addr", "replace", request.Address, "dev", request.Name})
	for _, address := range state.Addresses {
		if address != request.Address {
			commands = append(commands, []string{"addr", "del", address, "dev", request.Name})
		}
	}
	commands = append(commands, []string{"link", "set", request.Name, "up"})
	for _, route := range request.Routes {
		commands = append(commands, []string{"route", "replace", route, "dev", request.Name})
	}
	return commands
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
