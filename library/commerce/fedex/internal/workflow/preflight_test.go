// Copyright 2026 Trevin Chow and contributors. Licensed under Apache-2.0. See LICENSE.

package workflow

import "testing"

func TestValidatePickupAvailabilityBinding(t *testing.T) {
	address := map[string]any{"streetLines": []any{"1 Test Way"}, "city": "Austin", "stateOrProvinceCode": "TX", "postalCode": "78701", "countryCode": "US"}
	schedule := map[string]any{
		"associatedAccountNumber": map[string]any{"value": "123456789"},
		"carrierCode":             "FDXG",
		"packageCount":            1,
		"totalWeight":             map[string]any{"units": "LB", "value": 2.0},
		"originDetail": map[string]any{
			"readyDateTimestamp": "2026-09-04T09:00:00-05:00",
			"customerCloseTime":  "17:00",
			"pickupLocation": map[string]any{
				"contact": map[string]any{"personName": "Test User", "phoneNumber": "5555550100"},
				"address": address,
			},
		},
	}
	availability := map[string]any{
		"associatedAccountNumber": map[string]any{"value": "123456789"},
		"carriers":                []any{"FDXG"},
		"dispatchDate":            "2026-09-04",
		"packageReadyTime":        "09:00",
		"customerCloseTime":       "17:00",
		"pickupAddress":           address,
	}
	if err := ValidatePickupAvailabilityBinding(schedule, availability); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	availability["carriers"] = []any{"FDXG", "FDXE"}
	if err := ValidatePickupAvailabilityBinding(schedule, availability); err == nil {
		t.Fatal("multi-carrier availability request accepted")
	}
	availability["carriers"] = []any{"FDXG"}
	availability["dispatchDate"] = "2026-09-05"
	if err := ValidatePickupAvailabilityBinding(schedule, availability); err == nil {
		t.Fatal("mismatched dispatch date accepted")
	}
}

func TestPickupAvailabilityEvidenceRejectsConflictingEntries(t *testing.T) {
	if _, _, err := pickupAvailable([]byte(`{"options":[{"available":false},{"available":true}]}`)); err == nil {
		t.Fatal("conflicting availability booleans accepted")
	}
	if _, _, err := pickupWindowFields([]byte(`{"options":[{"cutoffTime":"16:00"},{"cutoffTime":"18:00"}]}`)); err == nil {
		t.Fatal("multiple unmatched cutoff times accepted")
	}
}
