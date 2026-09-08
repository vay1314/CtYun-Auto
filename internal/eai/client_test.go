package eai

import (
	"encoding/json"
	"testing"
)

func TestParseTenantsSupportsDirectAndNestedData(t *testing.T) {
	for name, raw := range map[string]string{
		"direct": `[{"tenantId":42,"tenantIdStr":"tenant-42"}]`,
		"nested": `{"data":[{"tenantId":42,"tenantIdStr":"tenant-42"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			values, err := parseTenants(json.RawMessage(raw))
			if err != nil {
				t.Fatal(err)
			}
			if len(values) != 1 || values[0].TenantID != 42 || values[0].HeaderID() != "tenant-42" {
				t.Fatalf("tenants = %#v", values)
			}
		})
	}
}

func TestTenantHeaderIDFallsBackToNumericID(t *testing.T) {
	if got := (Tenant{TenantID: 42}).HeaderID(); got != "42" {
		t.Fatalf("HeaderID() = %q", got)
	}
}
