// Package dotenv reads the deliberately small dotenv dialect emitted by ANAS.
// It is shared by artifact lifecycle and adapter-facing Module Command code so
// both interpret a frozen deployment identically.
package dotenv

import (
	"fmt"
	"os"
	"strings"
)

func ParseFile(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for index, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		equals := strings.Index(line, "=")
		if equals <= 0 {
			return nil, fmt.Errorf("%s:%d: invalid env line", path, index+1)
		}
		value, err := Unquote(line[equals+1:])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, index+1, err)
		}
		env[line[:equals]] = value
	}
	return env, nil
}

func Unquote(raw string) (string, error) {
	if len(raw) >= 2 && strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") {
		inner := raw[1 : len(raw)-1]
		if strings.Contains(inner, "'") {
			return "", fmt.Errorf("invalid single-quoted value %q", raw)
		}
		return inner, nil
	}
	if len(raw) >= 2 && strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) {
		inner := raw[1 : len(raw)-1]
		var builder strings.Builder
		for index := 0; index < len(inner); index++ {
			character := inner[index]
			switch character {
			case '\\':
				if index+1 >= len(inner) {
					return "", fmt.Errorf("trailing backslash in %q", raw)
				}
				index++
				switch inner[index] {
				case '\\':
					builder.WriteByte('\\')
				case '"':
					builder.WriteByte('"')
				case 'n':
					builder.WriteByte('\n')
				case 'r':
					builder.WriteByte('\r')
				default:
					return "", fmt.Errorf("unsupported escape \\%c in %q", inner[index], raw)
				}
			case '$':
				if index+1 < len(inner) && inner[index+1] == '$' {
					index++
				}
				builder.WriteByte('$')
			default:
				builder.WriteByte(character)
			}
		}
		return builder.String(), nil
	}
	return raw, nil
}
