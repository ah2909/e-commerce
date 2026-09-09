package token

import "testing"

func TestGenerateProducesOpaqueUniqueTokensAndMatchingDigests(t *testing.T) {
	first, firstDigest, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != 43 {
		t.Fatalf("unexpected generated tokens")
	}
	parsed, err := Parse(first)
	if err != nil || parsed != firstDigest {
		t.Fatalf("Parse() = %x, %v", parsed, err)
	}
}

func TestParseRejectsMalformedTokens(t *testing.T) {
	for _, value := range []string{"", "short", "___________________________________________="} {
		if _, err := Parse(value); err == nil {
			t.Errorf("Parse(%q) succeeded", value)
		}
	}
}
