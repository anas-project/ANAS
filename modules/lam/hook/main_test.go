package main

import "testing"

func TestLAMLanguageUsesDeclaredPOSIXLocale(t *testing.T) {
	for requested, want := range map[string]string{
		"zh-CN": "zh_CN.utf8",
		"zh-HK": "zh_TW.utf8",
		"pt-BR": "pt_BR.utf8",
		"en-SG": "en_GB.utf8",
	} {
		env := map[string]string{"DEFAULT_LANGUAGE": requested}
		if _, err := calcLAM(env, "", nil); err != nil {
			t.Fatalf("%s: %v", requested, err)
		}
		if got := env["LAM_LANGUAGE"]; got != want {
			t.Errorf("%s -> %s, want %s", requested, got, want)
		}
	}
}

func TestLAMUnsupportedLanguageFallsBackToEnglish(t *testing.T) {
	env := map[string]string{"DEFAULT_LANGUAGE": "cy-GB"}
	warnings, err := calcLAM(env, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("unsupported inherited language warnings = %v", warnings)
	}
	if got := env["LAM_LANGUAGE"]; got != "en_GB.utf8" && got != "en_US.utf8" {
		t.Fatalf("unsupported language fallback = %q", got)
	}
}

func TestLAMWarnsAndFallsBackForExplicitUnsupportedLanguage(t *testing.T) {
	env := map[string]string{"LAM_LANGUAGE": "cy-GB", "DEFAULT_LANGUAGE": "de-DE"}
	warnings, err := calcLAM(env, "", nil)
	if err != nil {
		t.Fatalf("explicit unsupported language blocked processing: %v", err)
	}
	if len(warnings) != 1 || env["LAM_LANGUAGE"] == "cy-GB" {
		t.Fatalf("warnings = %v, fallback = %q", warnings, env["LAM_LANGUAGE"])
	}
}
