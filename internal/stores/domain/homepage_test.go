package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestHomepageValidation(t *testing.T) {
	hero := HomepageSection{ID: "hero", Type: "hero", Layout: new("split")}
	story := HomepageSection{ID: "intro", Type: "story", Title: new("Về cửa hàng"), Body: new("Câu chuyện của chúng tôi"), ImageURL: new("https://images.example.test/photo.jpg"), ImageAlt: new("Cửa hàng")}
	featured := HomepageSection{ID: "featured", Type: "featured_products", Title: new("Nổi bật"), CategorySlug: new("new-items")}
	custom := func(sections ...HomepageSection) Homepage {
		return Homepage{SchemaVersion: 1, Mode: "custom", Sections: sections}
	}
	for _, tc := range []struct {
		name string
		page Homepage
	}{
		{"legacy", DefaultHomepage()},
		{"custom", custom(featured, hero, story)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.page.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	alter := func(section HomepageSection, change func(*HomepageSection)) HomepageSection {
		change(&section)
		return section
	}
	tooLarge := []HomepageSection{hero}
	for i := range 11 {
		section := story
		section.ID = string(rune('a' + i))
		section.Body = new(strings.Repeat("𐐷", 1000))
		tooLarge = append(tooLarge, section)
	}
	for _, tc := range []struct {
		name string
		page Homepage
	}{
		{"custom without hero", custom(story)},
		{"default with sections", Homepage{SchemaVersion: 1, Mode: "default", Sections: []HomepageSection{hero}}},
		{"unsupported version", Homepage{SchemaVersion: 2, Mode: "default"}},
		{"hidden hero", custom(alter(hero, func(s *HomepageSection) { s.Hidden = true }))},
		{"repeated hero", custom(hero, alter(hero, func(s *HomepageSection) { s.ID = "hero-2" }))},
		{"duplicate ID", custom(hero, story, story)},
		{"invalid ID", custom(alter(hero, func(s *HomepageSection) { s.ID = "../hero" }))},
		{"invalid hero layout", custom(alter(hero, func(s *HomepageSection) { s.Layout = new("float") }))},
		{"misplaced setting", custom(alter(hero, func(s *HomepageSection) { s.Title = new("hello") }))},
		{"unknown section", custom(hero, HomepageSection{ID: "custom", Type: "unknown"})},
		{"unsafe image", custom(hero, alter(story, func(s *HomepageSection) { s.ImageURL = new("http://example.test/a.png") }))},
		{"image missing description", custom(hero, alter(story, func(s *HomepageSection) { s.ImageAlt = nil }))},
		{"plain text", custom(hero, alter(story, func(s *HomepageSection) { s.Body = new("<b>hello</b>") }))},
		{"invalid category", custom(hero, alter(featured, func(s *HomepageSection) { s.CategorySlug = new("a/b") }))},
		{"long text", custom(hero, alter(story, func(s *HomepageSection) { s.Body = new(strings.Repeat("ế", 1001)) }))},
		{"aggregate content size", custom(tooLarge...)},
		{"too many sections", custom(make([]HomepageSection, 13)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.page.Validate(); !errors.Is(err, ErrInvalidHomepage) {
				t.Fatalf("got %v, want invalid homepage", err)
			}
		})
	}
}
