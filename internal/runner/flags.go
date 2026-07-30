package runner

import "flag"

// parseInterspersed parses args allowing flags to appear after positional
// arguments. The standard flag package stops at the first non-flag word, which
// would make the natural `anas rollback <ID> -w <workspace>` fail with a usage
// error while `anas rollback -w <workspace> <ID>` succeeded. Every command that
// takes a positional argument also takes -w, so the two orders have to behave
// the same.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
}
