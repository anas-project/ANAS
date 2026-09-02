package runner

import (
	"context"
	"os"
	"sort"

	"github.com/anas-project/ANAS/internal/compose"
)

func detectComposeForExecution(ctx context.Context, restricted bool) (compose.CLI, error) {
	if !restricted {
		return compose.Detect()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return compose.DetectContext(ctx, restrictedBaseProcessEnvironment())
}

func restrictedBaseProcessEnvironment() []string {
	values := map[string]string{}
	for _, key := range []string{"PATH", "HOME", "LANG"} {
		if value := os.Getenv(key); value != "" {
			values[key] = value
		}
	}
	if values["PATH"] == "" {
		values["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	return environmentMap(values)
}

func (a *app) commandEnvironment(deployment map[string]string) []string {
	if a == nil || !a.restrictedProcessEnvironment {
		result := append([]string(nil), os.Environ()...)
		keys := sortedEnvironmentKeys(deployment)
		for _, key := range keys {
			result = append(result, key+"="+deployment[key])
		}
		return result
	}
	values := map[string]string{}
	for _, assignment := range restrictedBaseProcessEnvironment() {
		for index := 0; index < len(assignment); index++ {
			if assignment[index] == '=' {
				values[assignment[:index]] = assignment[index+1:]
				break
			}
		}
	}
	for key, value := range deployment {
		// These variables select host executables or process-owned locations and
		// locales. A rendered workspace value must not be able to redirect daemon
		// subprocess lookup or host file discovery.
		if key == "PATH" || key == "HOME" || key == "LANG" {
			continue
		}
		values[key] = value
	}
	return environmentMap(values)
}

func environmentMap(values map[string]string) []string {
	keys := sortedEnvironmentKeys(values)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func sortedEnvironmentKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
