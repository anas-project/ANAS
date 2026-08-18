// Package localization normalizes deployment language, locale, and timezone
// settings at the boundary between host configuration and module-specific
// formats.
package localization

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	_ "time/tzdata"

	"golang.org/x/text/language"
)

// SystemDefaults is the localization state inherited when the corresponding
// global setting is absent. Values are already normalized for ANAS: timezone
// is an IANA name and language/locale are BCP 47 tags.
type SystemDefaults struct {
	Timezone string
	Language string
	Locale   string
}

// CurrentSystemDefaults reads the current host without mutating it. Explicit
// configuration remains preferable for reproducible deployments; inheritance
// exists so a freshly initialized workspace behaves like its host.
func CurrentSystemDefaults() SystemDefaults {
	locale := firstLocale(os.Getenv("LC_ALL"), os.Getenv("LC_TIME"), os.Getenv("LANG"))
	languageValue := firstLocale(os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG"))
	if runtime.GOOS == "darwin" {
		if apple := appleLocale(); apple != "" {
			if locale == "" || isCLocale(locale) {
				locale = apple
			}
			if languageValue == "" || isCLocale(languageValue) {
				languageValue = apple
			}
		}
	}

	lang, err := NormalizeLanguage(languageValue)
	if err != nil || lang == "" {
		lang = "en-US"
	}
	loc, err := NormalizeLocale(locale)
	if err != nil || loc == "" {
		loc, err = DefaultLocale(lang, "")
		if err != nil {
			loc = "en-US"
		}
	}
	return SystemDefaults{
		Timezone: systemTimezone(),
		Language: lang,
		Locale:   loc,
	}
}

// LocaleFromExplicitLanguage returns a locale only when the input language
// explicitly names a region. A region-less language such as en or zh-Hans is
// not enough to choose regional date, number, or currency conventions.
func LocaleFromExplicitLanguage(value string) (string, bool, error) {
	normalized, err := NormalizeLanguage(value)
	if err != nil || normalized == "" {
		return "", false, err
	}
	tag := language.Make(normalized)
	_, confidence := tag.Region()
	return normalized, confidence == language.Exact, nil
}

// DefaultLocale implements the locale fallback policy after an explicitly
// configured default_locale has been ruled out: a language with an explicit
// region, then the host locale, then a CLDR likely-region inference, and
// finally en-US.
func DefaultLocale(languageValue, hostLocale string) (string, error) {
	if locale, ok, err := LocaleFromExplicitLanguage(languageValue); err != nil {
		return "", err
	} else if ok {
		return locale, nil
	}
	if locale, err := NormalizeLocale(hostLocale); err != nil {
		return "", err
	} else if locale != "" {
		return locale, nil
	}
	normalized, err := NormalizeLanguage(languageValue)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return "en-US", nil
	}
	tag := language.Make(normalized)
	region, confidence := tag.Region()
	if confidence == language.No {
		return "en-US", nil
	}
	base, _, _ := tag.Raw()
	return base.String() + "-" + region.String(), nil
}

// NormalizeLanguage accepts BCP 47 and common POSIX locale spellings and
// returns the canonical BCP 47 representation supplied by x/text/CLDR.
func NormalizeLanguage(value string) (string, error) {
	return normalizeTag(value, "language")
}

// NormalizeLocale uses the same canonical representation as language while
// preserving its separate meaning: locale controls regional formatting, not
// UI translation selection.
func NormalizeLocale(value string) (string, error) {
	return normalizeTag(value, "locale")
}

func normalizeTag(value, field string) (string, error) {
	value = cleanPOSIXLocale(value)
	if value == "" {
		return "", nil
	}
	if isCLocale(value) {
		return "en-US", nil
	}
	tag, err := language.Parse(strings.ReplaceAll(value, "_", "-"))
	if err != nil {
		return "", fmt.Errorf("invalid %s %q: %w", field, value, err)
	}
	return tag.String(), nil
}

// ValidateTimezone accepts only names understood by Go's embedded/current
// IANA timezone database. Fixed offsets and POSIX TZ expressions are excluded
// because modules consistently consume region names through TZ.
func ValidateTimezone(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(value, ":"))
	if value == "" {
		return "", nil
	}
	// time.LoadLocation treats "Local" as a process-local special case rather
	// than an IANA database entry. Passing it through as TZ=Local is not
	// portable to containers, where a zoneinfo file named Local usually does
	// not exist.
	if value == "Local" || filepath.IsAbs(value) || strings.Contains(value, "..") {
		return "", fmt.Errorf("timezone must be an IANA name, got %q", value)
	}
	if _, err := time.LoadLocation(value); err != nil {
		return "", fmt.Errorf("invalid IANA timezone %q: %w", value, err)
	}
	return value, nil
}

// Target pairs an ANAS BCP 47 language with the value expected by an upstream
// application, such as LAM's de_DE.utf8.
type Target struct {
	Language string
	Value    string
}

// Match selects an upstream target with x/text's CLDR-aware matcher. It
// rejects a cross-script match (for example zh-Hant to zh-Hans) and then uses
// the caller's explicit fallback.
func Match(requested string, targets []Target, fallback string) (string, language.Confidence, error) {
	want, err := NormalizeLanguage(requested)
	if err != nil {
		return "", language.No, err
	}
	if want == "" {
		want = fallback
	}
	tags := make([]language.Tag, 0, len(targets))
	for _, target := range targets {
		tag, err := language.Parse(target.Language)
		if err != nil {
			return "", language.No, fmt.Errorf("invalid supported language %q: %w", target.Language, err)
		}
		tags = append(tags, tag)
	}
	if len(tags) == 0 {
		return "", language.No, fmt.Errorf("no supported languages declared")
	}
	wantTag := language.Make(want)
	matched, index, confidence := language.NewMatcher(tags).Match(wantTag)
	wantBase, wantBaseConfidence := wantTag.Base()
	matchedBase, matchedBaseConfidence := matched.Base()
	if wantBaseConfidence != language.No && matchedBaseConfidence != language.No && wantBase != matchedBase {
		confidence = language.No
	}
	wantScript, wantConfidence := wantTag.Script()
	matchedScript, matchedConfidence := matched.Script()
	if wantConfidence != language.No && matchedConfidence != language.No && wantScript != matchedScript {
		confidence = language.No
	}
	if confidence == language.No {
		fallbackTag := language.Make(fallback)
		_, index, _ = language.NewMatcher(tags).Match(fallbackTag)
	}
	value := targets[index].Value
	if value == "" {
		value = targets[index].Language
	}
	return value, confidence, nil
}

// Underscore converts a canonical tag to the common application spelling
// that uses an underscore before the region. It intentionally does not append
// a character encoding; applications that need a POSIX locale use Target.
func Underscore(value string) (string, error) {
	normalized, err := NormalizeLocale(value)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(normalized, "-", "_"), nil
}

func cleanPOSIXLocale(value string) string {
	value = strings.TrimSpace(value)
	if cut := strings.IndexByte(value, '.'); cut >= 0 {
		value = value[:cut]
	}
	if cut := strings.IndexByte(value, '@'); cut >= 0 {
		value = value[:cut]
	}
	return value
}

func firstLocale(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func isCLocale(value string) bool {
	value = strings.ToUpper(cleanPOSIXLocale(value))
	return value == "C" || value == "POSIX"
}

func appleLocale() string {
	cmd := exec.Command("/usr/bin/defaults", "read", "-g", "AppleLocale")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func systemTimezone() string {
	for _, candidate := range []string{os.Getenv("TZ"), readTimezoneFile("/etc/timezone"), localtimeZone()} {
		if zone, err := ValidateTimezone(candidate); err == nil && zone != "" {
			return zone
		}
	}
	if name := time.Now().Location().String(); name != "" && name != "Local" {
		if zone, err := ValidateTimezone(name); err == nil {
			return zone
		}
	}
	return "UTC"
}

func readTimezoneFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func localtimeZone() string {
	target, err := filepath.EvalSymlinks("/etc/localtime")
	if err != nil {
		return ""
	}
	target = filepath.ToSlash(target)
	for _, marker := range []string{"/zoneinfo/", "/timezone/zoneinfo/"} {
		if index := strings.LastIndex(target, marker); index >= 0 {
			return target[index+len(marker):]
		}
	}
	return ""
}
