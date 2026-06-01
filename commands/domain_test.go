package commands

import "testing"

func TestValidateDomainAllowsLeadingWildcard(t *testing.T) {
	if err := validateDomain("*.gatherpro.events"); err != nil {
		t.Fatalf("expected wildcard domain to be valid: %v", err)
	}
}

func TestValidateDomainRejectsInvalidWildcard(t *testing.T) {
	for _, domain := range []string{"gather*.events", "*.*.events", "api.*.events"} {
		t.Run(domain, func(t *testing.T) {
			if err := validateDomain(domain); err == nil {
				t.Fatalf("expected %q to be invalid", domain)
			}
		})
	}
}
