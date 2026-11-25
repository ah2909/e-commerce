package domain

import (
	"errors"
	"fmt"
	"regexp"
)

const (
	HomepageMaxSections = 12
	HomepageMaxContent  = 32 << 10
	HomepageMaxVersion  = 9007199254740991
)

var (
	ErrInvalidHomepage = errors.New("invalid storefront homepage")
	homepageSectionID  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	homepageSlug       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type Homepage struct {
	SchemaVersion int               `json:"schema_version"`
	Mode          string            `json:"mode"`
	Sections      []HomepageSection `json:"sections,omitempty"`
}

type HomepageSection struct {
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	Hidden       bool    `json:"hidden,omitempty"`
	Layout       *string `json:"layout,omitempty"`
	Title        *string `json:"title,omitempty"`
	Body         *string `json:"body,omitempty"`
	ImageURL     *string `json:"image_url,omitempty"`
	ImageAlt     *string `json:"image_alt,omitempty"`
	CategorySlug *string `json:"category_slug,omitempty"`
	ButtonLabel  *string `json:"button_label,omitempty"`
}

type HomepageState struct {
	Draft     Homepage `json:"draft"`
	Published Homepage `json:"published"`
	Version   int64    `json:"version"`
}

func DefaultHomepage() Homepage {
	return Homepage{SchemaVersion: 1, Mode: "default"}
}

func (h Homepage) Validate() error {
	if h.SchemaVersion != 1 {
		return ErrInvalidHomepage
	}
	if h.Mode == "default" && len(h.Sections) == 0 {
		return nil
	}
	if h.Mode != "custom" || len(h.Sections) == 0 || len(h.Sections) > HomepageMaxSections {
		return ErrInvalidHomepage
	}
	ids := make(map[string]bool, len(h.Sections))
	heroes, contentBytes := 0, 0
	for i, section := range h.Sections {
		if !homepageSectionID.MatchString(section.ID) || ids[section.ID] || !section.validSettings() {
			return fmt.Errorf("%w: section %d", ErrInvalidHomepage, i+1)
		}
		ids[section.ID] = true
		if section.Type == "hero" {
			heroes++
		}
		for _, value := range []*string{section.Layout, section.Title, section.Body, section.ImageURL, section.ImageAlt, section.CategorySlug, section.ButtonLabel} {
			if value != nil {
				contentBytes += len(*value)
			}
		}
	}
	if heroes != 1 || contentBytes > HomepageMaxContent {
		return ErrInvalidHomepage
	}
	return nil
}

func (s HomepageSection) validSettings() bool {
	if s.Title != nil && !validAppearanceText(*s.Title, 120, true) ||
		s.Body != nil && !validAppearanceText(*s.Body, 1000, true) ||
		s.ImageURL != nil && (*s.ImageURL == "" || !validAppearanceImageURL(*s.ImageURL)) ||
		s.ImageAlt != nil && !validAppearanceText(*s.ImageAlt, 160, true) ||
		s.CategorySlug != nil && (len(*s.CategorySlug) > 120 || !homepageSlug.MatchString(*s.CategorySlug)) ||
		s.ButtonLabel != nil && !validAppearanceText(*s.ButtonLabel, 60, true) {
		return false
	}
	switch s.Type {
	case "hero":
		return !s.Hidden && s.Layout != nil && (*s.Layout == "split" || *s.Layout == "stacked") &&
			s.Title == nil && s.Body == nil && s.ImageURL == nil && s.ImageAlt == nil && s.CategorySlug == nil && s.ButtonLabel == nil
	case "categories":
		return s.Title != nil && s.Layout == nil && s.Body == nil && s.ImageURL == nil && s.ImageAlt == nil && s.CategorySlug == nil && s.ButtonLabel == nil
	case "featured_products":
		return s.Title != nil && s.Layout == nil && s.Body == nil && s.ImageURL == nil && s.ImageAlt == nil && s.ButtonLabel == nil
	case "story":
		return s.Title != nil && s.Body != nil && (s.ImageURL == nil) == (s.ImageAlt == nil) &&
			s.Layout == nil && s.CategorySlug == nil && s.ButtonLabel == nil
	case "call_to_action":
		return s.Title != nil && s.ButtonLabel != nil && s.Layout == nil && s.Body == nil && s.ImageURL == nil && s.ImageAlt == nil
	default:
		return false
	}
}
