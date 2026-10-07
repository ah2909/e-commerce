package domain

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidAppearance = errors.New("invalid storefront appearance")

type Appearance struct {
	DisplayName  string `json:"display_name"`
	Tagline      string `json:"tagline"`
	LogoURL      string `json:"logo_url"`
	HeroURL      string `json:"hero_url"`
	HeroTitle    string `json:"hero_title"`
	HeroText     string `json:"hero_text"`
	Announcement string `json:"announcement"`
	Preset       string `json:"preset"`
	Version      int64  `json:"version"`
}

func DefaultAppearance(name string) Appearance {
	return Appearance{DisplayName: name, Preset: "home", HeroTitle: "Những điều đẹp cho mỗi ngày", HeroText: "Khám phá những sản phẩm được cửa hàng lựa chọn dành cho bạn."}
}

func (a Appearance) Validate() error {
	if a.Preset != "home" && a.Preset != "fashion" && a.Preset != "sport" {
		return ErrInvalidAppearance
	}
	if a.Version < 0 || a.Version >= 9007199254740991 {
		return ErrInvalidAppearance
	}
	for _, field := range []struct {
		value    string
		limit    int
		required bool
	}{
		{a.DisplayName, 120, true}, {a.Tagline, 180, false}, {a.HeroTitle, 160, true}, {a.HeroText, 600, false}, {a.Announcement, 240, false},
	} {
		if !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > field.limit || (field.required && strings.TrimSpace(field.value) == "") || strings.ContainsAny(field.value, "<>") {
			return ErrInvalidAppearance
		}
		for _, r := range field.value {
			if unicode.IsControl(r) {
				return ErrInvalidAppearance
			}
		}
	}
	for _, raw := range []string{a.LogoURL, a.HeroURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "<>\\\r\n\t ") {
			return ErrInvalidAppearance
		}
	}
	return nil
}
