package config

import (
	"testing"
)

func TestParseClientRule(t *testing.T) {
	valid := []string{"10.0.1.0/24", "10.0.1.5", " 192.168.1.1 ", "fd00::/8", "fd00::1"}
	for _, rule := range valid {
		if _, err := ParseClientRule(rule); err != nil {
			t.Errorf("ParseClientRule(%q) error = %v, want nil", rule, err)
		}
	}

	invalid := []string{"", "not-an-ip", "10.0.0.0/33", "10.0.0/24", "example.com"}
	for _, rule := range invalid {
		if _, err := ParseClientRule(rule); err == nil {
			t.Errorf("ParseClientRule(%q) = nil error, want failure", rule)
		}
	}
}

func TestValidateServerClientRulesAndConcurrency(t *testing.T) {
	base := ServerConfig{
		NFSPort:        2049,
		NFSVersion:     "4",
		MetricsPort:    8080,
		HealthPort:     8081,
		DebugPort:      8082,
		MaxConnections: 1000,
		ReadTimeout:    "30s",
		WriteTimeout:   "30s",
	}

	cfg := base
	cfg.AllowedClients = []string{"10.0.1.0/24", "192.168.7.5"}
	cfg.NFSConcurrentHandlers = 32
	if err := validateServer(&cfg); err != nil {
		t.Fatalf("validateServer(valid) error = %v", err)
	}

	cfg = base
	cfg.AllowedClients = []string{"bogus"}
	if err := validateServer(&cfg); err == nil {
		t.Fatal("validateServer should reject invalid allowed_clients entries")
	}

	cfg = base
	cfg.NFSConcurrentHandlers = -1
	if err := validateServer(&cfg); err == nil {
		t.Fatal("validateServer should reject negative nfs_concurrent_handlers")
	}
}

// Made with Bob
