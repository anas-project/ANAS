package appcatalog

import (
	"strconv"
	"strings"

	"golang.org/x/text/language"
)

// SupportedLanguages are the user interface and translation keys. Built-in
// catalog text still needs only zh-CN and en; the others are optional.
var SupportedLanguages = []string{"zh-CN", "zh-TW", "en", "ja", "ko", "fr", "de", "es"}

func supportedLanguage(value string) string {
	tag, err := language.Parse(value)
	if err != nil {
		return "en"
	}
	base, _ := tag.Base()
	switch base.String() {
	case "zh":
		script, _ := tag.Script()
		region, _ := tag.Region()
		if script.String() == "Hant" || region.String() == "TW" || region.String() == "HK" || region.String() == "MO" {
			return "zh-TW"
		}
		return "zh-CN"
	case "en", "ja", "ko", "fr", "de", "es":
		return base.String()
	}
	return "en"
}

// SupportedKey reports whether a translation map may use this language key.
func SupportedKey(lang string) bool {
	for _, value := range SupportedLanguages {
		if value == lang {
			return true
		}
	}
	return false
}

// SelectLanguage advances to the next source only when one is absent.
// An unsupported selected language immediately resolves to English.
func SelectLanguage(selected, acceptLanguage, deploymentDefault string) string {
	if strings.TrimSpace(selected) != "" {
		return supportedLanguage(selected)
	}
	best := ""
	bestQuality := -1.0
	for _, item := range strings.Split(acceptLanguage, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		value := strings.TrimSpace(parts[0])
		if value == "" || value == "*" {
			continue
		}
		if _, err := language.Parse(value); err != nil {
			continue
		}
		quality := 1.0
		valid := true
		for _, parameter := range parts[1:] {
			key, raw, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || key != "q" {
				valid = false
				break
			}
			var err error
			quality, err = strconv.ParseFloat(raw, 64)
			if err != nil || !(quality >= 0 && quality <= 1) {
				valid = false
				break
			}
		}
		if valid && quality > 0 && quality > bestQuality {
			best = value
			bestQuality = quality
		}
	}
	if best != "" {
		return supportedLanguage(best)
	}
	if strings.TrimSpace(deploymentDefault) != "" {
		return supportedLanguage(deploymentDefault)
	}
	return "en"
}

func (text Text) Resolve(lang string) string {
	order := []string{lang, "en", "zh-CN"}
	if lang == "zh-TW" {
		// Traditional Chinese readers are closer to Simplified Chinese than English.
		order = []string{lang, "zh-CN", "en"}
	}
	for _, candidate := range order {
		if value := text[candidate]; value != "" {
			return value
		}
	}
	return ""
}
