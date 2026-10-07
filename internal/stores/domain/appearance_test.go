package domain

import (
	"strings"
	"testing"
)

func TestAppearanceValidation(t *testing.T) {
	valid := DefaultAppearance("Cửa hàng nhỏ")
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Appearance){
		func(a *Appearance) { a.DisplayName = " " },
		func(a *Appearance) { a.HeroTitle = strings.Repeat("ắ", 161) },
		func(a *Appearance) { a.Announcement = "<script>" },
		func(a *Appearance) { a.Tagline = "text\x00" },
		func(a *Appearance) { a.Preset = "unknown" },
		func(a *Appearance) { a.LogoURL = "javascript:alert(1)" },
		func(a *Appearance) { a.HeroURL = "https://user:password@example.com/a" },
		func(a *Appearance) { a.HeroURL = "http://example.com/a" },
		func(a *Appearance) { a.Version = -1 },
	} {
		a := valid
		mutate(&a)
		if a.Validate() == nil {
			t.Errorf("accepted invalid appearance: %+v", a)
		}
	}
	valid.LogoURL = "https://cdn.example.com/logo.png"
	valid.HeroURL = "https://cdn.example.com/hero.jpg?width=800"
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}
