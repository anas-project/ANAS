package computeingressruntime

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// finiteRouteHosts computes a conservative Host set for the small rule grammar
// used by ANAS: Host, Path, PathPrefix, parentheses, && and ||. nil inside the
// parser means any Host; an unconstrained final result is refused. Unknown or
// negated matchers are never silently treated as empty inventory.
func finiteRouteHosts(rule string) ([]string, error) {
	if len(rule) == 0 || len(rule) > 4096 || strings.ContainsAny(rule, "\r\n\x00") {
		return nil, fmt.Errorf("Traefik rule is empty, oversized or invalid")
	}
	p := routeRuleParser{text: rule}
	hosts, err := p.expression(0)
	p.space()
	if err != nil || p.pos != len(p.text) || hosts == nil {
		return nil, fmt.Errorf("Traefik rule has no provable finite Host scope")
	}
	return slices.Sorted(maps.Keys(hosts)), nil
}

type routeRuleParser struct {
	text string
	pos  int
}

func (p *routeRuleParser) expression(depth int) (map[string]bool, error) {
	left, err := p.conjunction(depth)
	if err != nil {
		return nil, err
	}
	for p.take("||") {
		right, err := p.conjunction(depth)
		if err != nil {
			return nil, err
		}
		if left == nil || right == nil {
			left = nil
		} else {
			maps.Copy(left, right)
		}
	}
	return left, nil
}

func (p *routeRuleParser) conjunction(depth int) (map[string]bool, error) {
	left, err := p.atom(depth)
	if err != nil {
		return nil, err
	}
	for p.take("&&") {
		right, err := p.atom(depth)
		if err != nil {
			return nil, err
		}
		if left == nil {
			left = right
		} else if right != nil {
			for host := range left {
				if !right[host] {
					delete(left, host)
				}
			}
		}
	}
	return left, nil
}

func (p *routeRuleParser) atom(depth int) (map[string]bool, error) {
	if depth > 16 {
		return nil, fmt.Errorf("Traefik rule exceeds the nesting limit")
	}
	if p.take("(") {
		hosts, err := p.expression(depth + 1)
		if err != nil || !p.take(")") {
			return nil, fmt.Errorf("invalid Traefik rule grouping")
		}
		return hosts, nil
	}
	p.space()
	start := p.pos
	for p.pos < len(p.text) && ((p.text[p.pos] >= 'A' && p.text[p.pos] <= 'Z') || (p.text[p.pos] >= 'a' && p.text[p.pos] <= 'z')) {
		p.pos++
	}
	name := p.text[start:p.pos]
	if (name != "Host" && name != "Path" && name != "PathPrefix") || !p.take("(") {
		return nil, fmt.Errorf("unsupported Traefik rule matcher")
	}
	value, err := p.literal()
	if err != nil || !p.take(")") {
		return nil, fmt.Errorf("Traefik matcher requires one literal argument")
	}
	if name == "Host" {
		value = strings.ToLower(value)
		if !validHTTPHost(value) {
			return nil, fmt.Errorf("Traefik Host matcher is not a canonical DNS name")
		}
		return map[string]bool{value: true}, nil
	}
	if !strings.HasPrefix(value, "/") {
		return nil, fmt.Errorf("invalid Traefik path matcher")
	}
	return nil, nil
}

func (p *routeRuleParser) literal() (string, error) {
	p.space()
	start := p.pos
	if start == len(p.text) || (p.text[start] != '`' && p.text[start] != '"') {
		return "", fmt.Errorf("Traefik matcher argument is not quoted")
	}
	quote := p.text[p.pos]
	p.pos++
	for p.pos < len(p.text) {
		c := p.text[p.pos]
		p.pos++
		if c == '\\' && quote == '"' {
			if p.pos < len(p.text) {
				p.pos++
			}
			continue
		}
		if c == quote {
			value, err := strconv.Unquote(p.text[start:p.pos])
			if err != nil || len(value) == 0 || strings.ContainsAny(value, "\r\n\x00") {
				return "", fmt.Errorf("invalid Traefik matcher literal")
			}
			return value, nil
		}
	}
	return "", fmt.Errorf("unterminated Traefik matcher literal")
}

func (p *routeRuleParser) take(value string) bool {
	p.space()
	if !strings.HasPrefix(p.text[p.pos:], value) {
		return false
	}
	p.pos += len(value)
	return true
}

func (p *routeRuleParser) space() {
	for p.pos < len(p.text) && (p.text[p.pos] == ' ' || p.text[p.pos] == '\t') {
		p.pos++
	}
}
