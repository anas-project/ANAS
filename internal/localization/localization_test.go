package localization

import (
	"testing"

	"golang.org/x/text/language"
)

func TestNormalizePOSIXAndBCP47(t *testing.T) {
	for input, want := range map[string]string{
		"zh_CN.UTF-8": "zh-CN",
		"zh-cn":       "zh-CN",
		"pt_BR@euro":  "pt-BR",
		"C.UTF-8":     "en-US",
	} {
		got, err := NormalizeLanguage(input)
		if err != nil {
			t.Fatalf("NormalizeLanguage(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCurrentSystemDefaultsUseHostValues(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LC_TIME", "")
	t.Setenv("LANG", "pt_BR.UTF-8")
	got := CurrentSystemDefaults()
	if got.Timezone != "Asia/Tokyo" || got.Language != "pt-BR" || got.Locale != "pt-BR" {
		t.Fatalf("system defaults = %+v", got)
	}
}

func TestCurrentSystemDefaultsKeepsHostLocaleSeparateFromHostLanguage(t *testing.T) {
	t.Setenv("TZ", "UTC")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "en_US.UTF-8")
	t.Setenv("LC_TIME", "pt_BR.UTF-8")
	t.Setenv("LANG", "de_DE.UTF-8")
	got := CurrentSystemDefaults()
	if got.Language != "en-US" || got.Locale != "pt-BR" {
		t.Fatalf("system defaults = %+v, want independent host language and locale", got)
	}
}

func TestDefaultLocalePrecedence(t *testing.T) {
	for _, test := range []struct {
		language, host, want string
	}{
		{language: "en-GB", host: "pt_BR.UTF-8", want: "en-GB"},
		{language: "zh-Hant-TW", host: "en_US.UTF-8", want: "zh-Hant-TW"},
		{language: "zh-Hans", host: "en_SG.UTF-8", want: "en-SG"},
		{language: "zh-Hans", host: "", want: "zh-CN"},
		{language: "", host: "", want: "en-US"},
	} {
		got, err := DefaultLocale(test.language, test.host)
		if err != nil || got != test.want {
			t.Errorf("DefaultLocale(%q, %q) = %q, %v; want %q", test.language, test.host, got, err, test.want)
		}
	}
}

func TestTimezoneValidation(t *testing.T) {
	if got, err := ValidateTimezone("Asia/Shanghai"); err != nil || got != "Asia/Shanghai" {
		t.Fatalf("valid timezone = %q, %v", got, err)
	}
	for _, bad := range []string{"Local", "UTC+8", "/etc/localtime", "../zone"} {
		if _, err := ValidateTimezone(bad); err == nil {
			t.Errorf("invalid timezone %q was accepted", bad)
		}
	}
}

func TestMatchUsesUpstreamValueAndKeepsScript(t *testing.T) {
	targets := []Target{
		{Language: "en-US", Value: "en_US.utf8"},
		{Language: "zh-Hans", Value: "zh_CN.utf8"},
		{Language: "zh-Hant", Value: "zh_TW.utf8"},
	}
	got, confidence, err := Match("zh-HK", targets, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if got != "zh_TW.utf8" || confidence == language.No {
		t.Fatalf("zh-HK matched %q (%v), want traditional Chinese", got, confidence)
	}

	got, confidence, err = Match("sr-Latn", targets, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	if got != "en_US.utf8" {
		t.Fatalf("cross-script unsupported language matched %q (%v), want fallback", got, confidence)
	}
	if confidence != language.No {
		t.Fatalf("fallback confidence = %v, want language.No so callers can reject an explicit unsupported value", confidence)
	}
}

func TestUnderscore(t *testing.T) {
	got, err := Underscore("pt-BR")
	if err != nil || got != "pt_BR" {
		t.Fatalf("Underscore = %q, %v", got, err)
	}
}
