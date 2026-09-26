package incusingresshost

import (
	"fmt"
	"slices"
	"strings"
)

// These are conjunctions with one final ACCEPT. xtables can reorder match
// modules; it cannot add negation, unknown options, side effects or verdicts.
func forwardingCompatSignature(words []string) (string, error) {
	if len(words) < 4 || words[0] != "-A" {
		return "", fmt.Errorf("invalid owned filter rule")
	}
	values := map[string]string{"chain": words[1]}
	modules := map[string]bool{}
	for i := 2; i < len(words); i++ {
		key := words[i]
		if i+1 >= len(words) {
			return "", fmt.Errorf("incomplete owned filter option")
		}
		i++
		value := words[i]
		if key == "-m" {
			if !slices.Contains([]string{"tcp", "conntrack", "set", "comment"}, value) || modules[value] {
				return "", fmt.Errorf("unsupported owned filter match")
			}
			modules[value] = true
			continue
		}
		if !slices.Contains([]string{"-s", "-d", "-i", "-o", "-p", "--sport", "--dport", "--ctstate", "--ctdir", "--match-set", "--comment", "-j"}, key) {
			return "", fmt.Errorf("unknown owned filter option")
		}
		if _, duplicate := values[key]; duplicate {
			return "", fmt.Errorf("duplicate owned filter option")
		}
		if key == "--match-set" {
			if i+1 >= len(words) {
				return "", fmt.Errorf("missing owned set dimensions")
			}
			i++
			value += "\x00" + words[i]
		}
		if key == "--ctstate" {
			states := strings.Split(value, ",")
			slices.Sort(states)
			value = strings.Join(states, ",")
		}
		values[key] = value
	}
	return forwardingHash(struct {
		Values  map[string]string
		Modules map[string]bool
	}{values, modules}), nil
}

func verifyForwardingCompat(in forwardingFilterInventory, scope ForwardingKernelScope, present bool) error {
	chain := forwardingCompatChain(scope)
	policy, exists := in.Chains[chain]
	if !present {
		if exists {
			return fmt.Errorf("unowned forwarding compatibility chain already exists")
		}
	} else {
		if !exists || policy != "-" {
			return fmt.Errorf("owned forwarding chain is absent or replaced")
		}
		want := forwardingCompatRules(scope)
		got := in.Rules[chain]
		if len(got) != len(want) {
			return fmt.Errorf("owned forwarding chain rule count changed")
		}
		for i := range want {
			a, e1 := forwardingCompatSignature(got[i])
			b, e2 := forwardingCompatSignature(want[i])
			if e1 != nil || e2 != nil || a != b {
				return fmt.Errorf("owned forwarding compatibility predicate changed")
			}
		}
	}
	jumps := 0
	for parent, rules := range in.Rules {
		for _, rule := range rules {
			for i, word := range rule {
				if (word == "-j" || word == "-g") && i+1 < len(rule) && rule[i+1] == chain {
					if !present || parent != "FORWARD" || !slices.Equal(rule, forwardingJump(scope)) {
						return fmt.Errorf("owned forwarding chain has a foreign reference")
					}
					jumps++
				}
			}
		}
	}
	if present && jumps != 1 {
		return fmt.Errorf("owned forwarding jump is missing or duplicated")
	}
	return nil
}

func verifyForwardingTail(in forwardingFilterInventory, known []ForwardingKernelScope) error {
	if in.Chains["FORWARD"] != "DROP" {
		return fmt.Errorf("default Docker FORWARD DROP is required")
	}
	allowed := map[string][]string{}
	for _, scope := range known {
		if scope.Validate() != nil {
			return fmt.Errorf("invalid installed forwarding tail scope")
		}
		chain := forwardingCompatChain(scope)
		if allowed[chain] != nil {
			return fmt.Errorf("duplicate installed forwarding tail scope")
		}
		allowed[chain] = forwardingJump(scope)
	}
	insideTail := false
	seen := map[string]bool{}
	for _, rule := range in.Rules["FORWARD"] {
		owned := ""
		for i, word := range rule {
			if word == "-j" && i+1 < len(rule) && allowed[rule[i+1]] != nil {
				owned = rule[i+1]
				break
			}
		}
		if owned != "" {
			if seen[owned] || !slices.Equal(rule, allowed[owned]) {
				return fmt.Errorf("ambiguous installed forwarding tail")
			}
			seen[owned] = true
			insideTail = true
		} else if insideTail {
			return fmt.Errorf("administrator rules follow the owned forwarding tail; renewal is refused")
		}
	}
	return nil
}
