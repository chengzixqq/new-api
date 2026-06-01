package service

import (
	"testing"
	"time"
)

func TestParseUpstreamWarmupURLs(t *testing.T) {
	raw := " https://a.example/v1/models,https://b.example ;\nhttps://c.example\t https://d.example "
	got := parseUpstreamWarmupURLs(raw)
	want := []string{
		"https://a.example/v1/models",
		"https://b.example",
		"https://c.example",
		"https://d.example",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d urls, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("url %d: expected %q, got %q", i, want[i], got[i])
		}
	}

	if got := parseUpstreamWarmupURLs(" \n\t ; , "); len(got) != 0 {
		t.Fatalf("expected empty urls for blank input, got %#v", got)
	}
}

func TestParseUpstreamWarmupDuration(t *testing.T) {
	const envName = "TEST_UPSTREAM_WARMUP_DURATION"
	fallback := 30 * time.Second

	t.Setenv(envName, "")
	if got := parseUpstreamWarmupDuration(envName, fallback); got != fallback {
		t.Fatalf("blank env: expected %s, got %s", fallback, got)
	}

	t.Setenv(envName, "25")
	if got := parseUpstreamWarmupDuration(envName, fallback); got != 25*time.Second {
		t.Fatalf("numeric env: expected 25s, got %s", got)
	}

	t.Setenv(envName, "1m")
	if got := parseUpstreamWarmupDuration(envName, fallback); got != time.Minute {
		t.Fatalf("duration env: expected 1m, got %s", got)
	}

	t.Setenv(envName, "not-a-duration")
	if got := parseUpstreamWarmupDuration(envName, fallback); got != fallback {
		t.Fatalf("invalid env: expected fallback %s, got %s", fallback, got)
	}
}

func TestParseUpstreamWarmupJitter(t *testing.T) {
	const envName = "TEST_UPSTREAM_WARMUP_JITTER"
	fallback := 0.2

	t.Setenv(envName, "")
	if got := parseUpstreamWarmupJitter(envName, fallback); got != fallback {
		t.Fatalf("blank env: expected %.2f, got %.2f", fallback, got)
	}

	t.Setenv(envName, "0.35")
	if got := parseUpstreamWarmupJitter(envName, fallback); got != 0.35 {
		t.Fatalf("valid env: expected 0.35, got %.2f", got)
	}

	for _, value := range []string{"-0.1", "0.6", "invalid"} {
		t.Setenv(envName, value)
		if got := parseUpstreamWarmupJitter(envName, fallback); got != fallback {
			t.Fatalf("invalid env %q: expected %.2f, got %.2f", value, fallback, got)
		}
	}
}

func TestMakeTargetFromURL_Dedup(t *testing.T) {
	seen := make(map[string]bool)

	first, ok := makeTargetFromURL("https://api.example.com/v1/models", "", seen)
	if !ok {
		t.Fatal("expected first target to be accepted")
	}
	if first.key != "https://api.example.com|" || first.host != "api.example.com" || first.proxy != "" {
		t.Fatalf("unexpected first target: %#v", first)
	}

	if _, ok := makeTargetFromURL("https://api.example.com/anything", "", seen); ok {
		t.Fatal("expected duplicate scheme/host/proxy target to be rejected")
	}

	proxied, ok := makeTargetFromURL("https://api.example.com/v1/models", "http://proxy.example:8080", seen)
	if !ok {
		t.Fatal("expected same host with different proxy to be accepted")
	}
	if proxied.key != "https://api.example.com|http://proxy.example:8080" || proxied.proxy != "http://proxy.example:8080" {
		t.Fatalf("unexpected proxied target: %#v", proxied)
	}
}

func TestMakeTargetFromURL_ForbiddenPath(t *testing.T) {
	seen := make(map[string]bool)
	for _, rawURL := range []string{
		"https://api.example.com/v1/chat/completions",
		"https://api.example.com/v1/responses",
		"https://api.example.com/v1/images/generations",
	} {
		if _, ok := makeTargetFromURL(rawURL, "", seen); ok {
			t.Fatalf("expected billable path %q to be rejected", rawURL)
		}
	}
}

func TestJoinWarmupPath(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{name: "both clean", base: "https://api.example.com", path: "v1/models", want: "https://api.example.com/v1/models"},
		{name: "base trailing slash", base: "https://api.example.com/", path: "v1/models", want: "https://api.example.com/v1/models"},
		{name: "path leading slash", base: "https://api.example.com", path: "/v1/models", want: "https://api.example.com/v1/models"},
		{name: "both slashes", base: "https://api.example.com/", path: "/v1/models", want: "https://api.example.com/v1/models"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinWarmupPath(tt.base, tt.path); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestWithJitter(t *testing.T) {
	d := 30 * time.Second
	if got := withJitter(d, 0); got != d {
		t.Fatalf("frac=0: expected %s, got %s", d, got)
	}

	frac := 0.2
	min := time.Duration(float64(d) * (1 - frac))
	max := time.Duration(float64(d) * (1 + frac))
	for i := 0; i < 100; i++ {
		got := withJitter(d, frac)
		if got < min || got > max {
			t.Fatalf("jitter result %s out of range [%s, %s]", got, min, max)
		}
	}
}
