package main

import "testing"

func TestLocalProbeURLRejectsNonLoopbackTargets(t *testing.T) {
	if _, err := localProbeURL("http://127.0.0.1:8080/health/ready"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"https://example.test/health/ready",
		"http://127.0.0.1:8080/health/live",
		"http://127.0.0.1:8080/health/ready?next=/",
		"http://user@127.0.0.1:8080/health/ready",
	} {
		if _, err := localProbeURL(target); err == nil {
			t.Fatalf("localProbeURL(%q) succeeded", target)
		}
	}
}
