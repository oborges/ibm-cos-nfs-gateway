package config

import (
	"fmt"
	"net"
	"strings"
)

// ParseClientRule parses an allowed_clients entry: either a CIDR
// ("10.0.1.0/24", "fd00::/8") or a single IP ("10.0.1.5"), which is treated
// as a host-sized network.
func ParseClientRule(rule string) (*net.IPNet, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return nil, fmt.Errorf("empty client rule")
	}

	if strings.Contains(rule, "/") {
		_, network, err := net.ParseCIDR(rule)
		if err != nil {
			return nil, fmt.Errorf("not a valid CIDR: %w", err)
		}
		return network, nil
	}

	ip := net.ParseIP(rule)
	if ip == nil {
		return nil, fmt.Errorf("not a valid IP address")
	}
	bits := 128
	if ip.To4() != nil {
		ip = ip.To4()
		bits = 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
}

// ParseClientRules parses a full allowed_clients list.
func ParseClientRules(rules []string) ([]*net.IPNet, error) {
	networks := make([]*net.IPNet, 0, len(rules))
	for _, rule := range rules {
		network, err := ParseClientRule(rule)
		if err != nil {
			return nil, fmt.Errorf("allowed_clients entry %q: %w", rule, err)
		}
		networks = append(networks, network)
	}
	return networks, nil
}

// Made with Bob
