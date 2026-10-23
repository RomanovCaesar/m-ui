package app

import (
	"strings"
	"testing"
)

func TestAndRulesValidateAndRender(t *testing.T) {
	outbounds := []MihomoOutbound{}
	valid := []string{
		"((IN-NAME,inbound-443),(DOMAIN-SUFFIX,google.com))",
		"((IN-USER,alice/bob),(OR,((GEOSITE,openai),(DOMAIN-KEYWORD,claude))))",
		"((NOT,((GEOIP,CN))),(DST-PORT,443/8443/1000-2000))",
		"((IP-CIDR,10.0.0.0/8,no-resolve),(NETWORK,udp))",
	}
	for _, payload := range valid {
		rules, err := normalizeMihomoRoutingRules([]MihomoRoutingRule{{Type: "AND", Payload: payload, Target: "REJECT"}}, outbounds)
		if err != nil {
			t.Fatalf("%s: %v", payload, err)
		}
		lines := mihomoRoutingConfig(rules)
		if lines[0] != "AND,"+payload+",REJECT" {
			t.Fatalf("rendered %q", lines[0])
		}
	}
	invalid := []string{
		"(IN-NAME,a)",
		"((IN-NAME,a),(MATCH,))",
		"((IN-NAME,a),(BOGUS,x))",
		"((IN-NAME,a),(DST-PORT,99999))",
		"((IN-NAME,a)",
		"((IN-NAME,a)) junk",
		"()",
	}
	for _, payload := range invalid {
		if _, err := normalizeMihomoRoutingRules([]MihomoRoutingRule{{Type: "AND", Payload: payload, Target: "REJECT"}}, outbounds); err == nil {
			t.Fatalf("accepted invalid AND payload %q", payload)
		}
	}
}

func TestPortRulesAcceptSlashLists(t *testing.T) {
	for payload, ok := range map[string]bool{"443": true, "1000-2000": true, "53/443/1000-2000": true, "443/": false, "70000": false, "2000-1000": false} {
		err := validateMihomoRulePayload(MihomoRoutingRule{Type: "DST-PORT", Payload: payload})
		if (err == nil) != ok {
			t.Fatalf("DST-PORT %q: err=%v, want ok=%v", payload, err, ok)
		}
	}
	if !strings.Contains(strings.Join(mihomoRuleTypes, ","), "AND") {
		t.Fatal("AND is not a known rule type")
	}
}
