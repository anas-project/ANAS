package incusingresshost

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type forwardingFilterInventory struct {
	Lines  []string
	Chains map[string]string
	Rules  map[string][][]string
}

var forwardingCounterHeader = regexp.MustCompile(`\[[0-9]+:[0-9]+\]$`)

// iptables-save is interpreted as data. No observed line is ever executed.
func forwardingWords(line string) ([]string, error) {
	if len(line) > 64<<10 || strings.ContainsAny(line, "\r\n\x00") {
		return nil, fmt.Errorf("invalid forwarding compatibility line")
	}
	var words []string
	var word strings.Builder
	quoted, escaped, started := false, false, false
	for _, c := range line {
		if c < 0x20 && c != '\t' || c > 0x7e {
			return nil, fmt.Errorf("unsupported compatibility output character")
		}
		if escaped {
			word.WriteRune(c)
			escaped = false
			started = true
			continue
		}
		if c == '\\' {
			escaped = true
			started = true
			continue
		}
		if c == '"' {
			quoted = !quoted
			started = true
			continue
		}
		if !quoted && (c == ' ' || c == '\t') {
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(c)
		started = true
	}
	if quoted || escaped {
		return nil, fmt.Errorf("incomplete compatibility output quoting")
	}
	if started {
		words = append(words, word.String())
	}
	return words, nil
}

func parseForwardingFilter(body []byte) (forwardingFilterInventory, error) {
	empty := forwardingFilterInventory{}
	if len(body) == 0 || len(body) > 1<<20 {
		return empty, fmt.Errorf("filter inventory is incomplete")
	}
	in := forwardingFilterInventory{Chains: map[string]string{}, Rules: map[string][][]string{}}
	opened, closed := false, false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if closed {
			return empty, fmt.Errorf("unexpected table after filter commit")
		}
		if line == "*filter" {
			if opened {
				return empty, fmt.Errorf("duplicate filter table")
			}
			opened = true
			in.Lines = append(in.Lines, line)
			continue
		}
		if !opened {
			return empty, fmt.Errorf("filter table header is absent")
		}
		if line == "COMMIT" {
			closed = true
			in.Lines = append(in.Lines, line)
			continue
		}
		if strings.HasPrefix(line, ":") {
			fields := strings.Fields(line)
			if len(fields) != 3 || !forwardingCounterHeader.MatchString(fields[2]) || len(fields[0]) < 2 {
				return empty, fmt.Errorf("invalid filter chain definition")
			}
			name := strings.TrimPrefix(fields[0], ":")
			if _, duplicate := in.Chains[name]; duplicate {
				return empty, fmt.Errorf("duplicate filter chain")
			}
			if fields[1] != "-" && fields[1] != "ACCEPT" && fields[1] != "DROP" {
				return empty, fmt.Errorf("unsupported filter chain policy")
			}
			in.Chains[name] = fields[1]
			in.Lines = append(in.Lines, fields[0]+" "+fields[1]+" [0:0]")
			continue
		}
		words, err := forwardingWords(line)
		if err != nil || len(words) < 4 || words[0] != "-A" {
			return empty, fmt.Errorf("unsupported filter inventory record")
		}
		if _, exists := in.Chains[words[1]]; !exists {
			return empty, fmt.Errorf("filter rule references an unknown chain")
		}
		in.Rules[words[1]] = append(in.Rules[words[1]], words)
		in.Lines = append(in.Lines, line)
	}
	if !opened || !closed || in.Chains["FORWARD"] == "" {
		return empty, fmt.Errorf("incomplete FORWARD inventory")
	}
	return in, nil
}

func forwardingJump(scope ForwardingKernelScope) []string {
	return []string{"-A", "FORWARD", "-m", "comment", "--comment", forwardingKernelComment(scope), "-j", forwardingCompatChain(scope)}
}

func forwardingCompatRules(scope ForwardingKernelScope) [][]string {
	result := [][]string{}
	chain, set, comment := forwardingCompatChain(scope), forwardingIPSetName(scope), forwardingKernelComment(scope)
	for _, route := range scope.Routes {
		port := strconv.Itoa(int(route.Port))
		result = append(result,
			[]string{"-A", chain, "-d", route.Destination + "/32", "-i", scope.Network.BridgeName, "-o", route.OutputName, "-p", "tcp", "-m", "tcp", "--dport", port,
				"-m", "conntrack", "--ctstate", "NEW,ESTABLISHED", "--ctdir", "ORIGINAL", "-m", "set", "--match-set", set, "src,dst,dst", "-m", "comment", "--comment", comment, "-j", "ACCEPT"},
			[]string{"-A", chain, "-s", route.Destination + "/32", "-i", route.OutputName, "-o", scope.Network.BridgeName, "-p", "tcp", "-m", "tcp", "--sport", port,
				"-m", "conntrack", "--ctstate", "ESTABLISHED", "--ctdir", "REPLY", "-m", "set", "--match-set", set, "dst,src,src", "-m", "comment", "--comment", comment, "-j", "ACCEPT"})
	}
	return result
}

// Only the fixed owned chain and its exact tail reference may be created or
// removed. No global policy, Docker chain, host rule or observed line is replayed.
func forwardingCompatInstall(scope ForwardingKernelScope) []byte {
	var out strings.Builder
	out.WriteString("*filter\n-N " + forwardingCompatChain(scope) + "\n")
	for _, rule := range forwardingCompatRules(scope) {
		out.WriteString(strings.Join(rule, " ") + "\n")
	}
	out.WriteString(strings.Join(forwardingJump(scope), " ") + "\nCOMMIT\n")
	return []byte(out.String())
}

func forwardingCompatRemove(scope ForwardingKernelScope) []byte {
	jump := forwardingJump(scope)
	jump[0] = "-D"
	return []byte("*filter\n" + strings.Join(jump, " ") + "\n-F " + forwardingCompatChain(scope) + "\n-X " + forwardingCompatChain(scope) + "\nCOMMIT\n")
}

func forwardingForeignFilterDigest(in forwardingFilterInventory, scope ForwardingKernelScope) string {
	chain := forwardingCompatChain(scope)
	out := []string{}
	for _, line := range in.Lines {
		if strings.HasPrefix(line, ":"+chain+" ") || strings.HasPrefix(line, "-A "+chain+" ") {
			continue
		}
		words, err := forwardingWords(line)
		if err == nil && slices.Equal(words, forwardingJump(scope)) {
			continue
		}
		out = append(out, line)
	}
	return forwardingHash(out)
}
