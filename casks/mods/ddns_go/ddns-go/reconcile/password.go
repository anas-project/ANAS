package main

// passwordMatches reports whether the stored credential already is the one
// ANAS intends.
//
// The desired value arrives as a bcrypt hash rather than a plaintext, so this
// process never holds the password itself. The hash is generated once by the
// cask hook and persisted in the secret store, which is what makes a plain
// comparison sufficient: bcrypt salts every hash differently, so recomputing
// it here would differ on every restart and rewrite the file each time.
func passwordMatches(stored, desired string) bool {
	return stored != "" && stored == desired
}
