// Command anas-helper performs the few operations anas cannot do as an
// ordinary user.
//
// It exists so that those operations can be granted individually. The
// alternative anas used to rely on -- a sudoers rule authorising root to run a
// shell script that anas itself had written into a user-writable directory --
// grants the user who runs anas everything root can do, which the runbook for
// that rule already admitted. This binary is root-owned, takes named
// operations rather than a script, and validates every argument before acting.
//
// It is meant to be installed with a file capability rather than setuid:
//
//	setcap cap_net_admin+ep /usr/local/lib/anas/anas-helper
//
// so it holds one capability instead of all of them. See
// docs/architecture/privilege-helper-draft.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "anas-helper: "+err.Error())
		os.Exit(1)
	}
}

const usage = `anas-helper performs the operations anas cannot do unprivileged.

  bridge up   --parent <iface> --name <bridge> --address <cidr> [--route <cidr>]...
              Create the macvlan bridge if absent, give it exactly --address,
              bring it up, and route each --route through it.

  bridge down --name <bridge>
              Delete the bridge.

Only interfaces named anas* can be touched, in either direction.`

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no operation given\n\n%s", usage)
	}
	switch args[0] {
	case "bridge":
		return runBridge(args[1:])
	case "help", "-h", "--help":
		fmt.Println(usage)
		return nil
	default:
		return fmt.Errorf("unknown operation %q\n\n%s", args[0], usage)
	}
}

func runBridge(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("bridge needs up or down\n\n%s", usage)
	}
	request, err := parseBridge(args)
	if err != nil {
		return err
	}
	return applyBridge(request)
}
