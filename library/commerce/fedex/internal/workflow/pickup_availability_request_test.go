// Copyright 2026 Trevin Chow and contributors. Licensed under Apache-2.0. See LICENSE.

package workflow

import "testing"

func TestValidatePickupAvailabilityRequestAcceptsOfficialGroundMinimum(t *testing.T) {
	request := map[string]any{
		"pickupAddress":       map[string]any{"postalCode": "38116", "countryCode": "US"},
		"pickupRequestType":   []any{"FUTURE_DAY"},
		"carriers":            []any{"FDXG"},
		"countryRelationship": "DOMESTIC",
	}
	if err := ValidatePickupAvailabilityRequest(request); err != nil {
		t.Fatalf("official Ground minimum rejected: %v", err)
	}
}

func TestValidatePickupAvailabilityRequestAcceptsNestedExpressDetails(t *testing.T) {
	request := map[string]any{
		"pickupAddress":           map[string]any{"postalCode": "75008", "countryCode": "FR"},
		"dispatchDate":            "2026-09-04",
		"packageReadyTime":        "15:30:00",
		"customerCloseTime":       "18:00:00",
		"pickupType":              "ON_CALL",
		"pickupRequestType":       []any{"SAME_DAY"},
		"carriers":                []any{"FDXE"},
		"countryRelationship":     "INTERNATIONAL",
		"associatedAccountNumber": "123456789",
		"shipmentAttributes": map[string]any{
			"serviceType":   "INTERNATIONAL_PRIORITY_FREIGHT",
			"weight":        map[string]any{"units": "KG", "value": 20},
			"packagingType": "YOUR_PACKAGING",
			"dimensions":    map[string]any{"length": 7, "width": 8, "height": 9, "units": "CM"},
		},
		"packageDetails": []any{map[string]any{
			"packageSpecialServices": map[string]any{"specialServiceTypes": []any{"SIGNATURE_OPTION"}},
		}},
	}
	if err := ValidatePickupAvailabilityRequest(request); err != nil {
		t.Fatalf("official nested availability request rejected: %v", err)
	}
}

func TestValidatePickupAvailabilityRequestRejectsMalformedNestedDetails(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"pickupAddress":       map[string]any{"postalCode": "38116", "countryCode": "US"},
			"pickupRequestType":   []any{"FUTURE_DAY"},
			"carriers":            []any{"FDXG"},
			"countryRelationship": "DOMESTIC",
		}
	}
	tests := map[string]func(map[string]any){
		"empty shipment attributes": func(request map[string]any) { request["shipmentAttributes"] = map[string]any{} },
		"missing service type": func(request map[string]any) {
			request["shipmentAttributes"] = map[string]any{"weight": map[string]any{"units": "LB", "value": 2}}
		},
		"incomplete weight": func(request map[string]any) {
			request["shipmentAttributes"] = map[string]any{"serviceType": "FEDEX_GROUND", "weight": map[string]any{"units": "LB"}}
		},
		"incomplete dimensions": func(request map[string]any) {
			request["shipmentAttributes"] = map[string]any{"serviceType": "FEDEX_GROUND", "dimensions": map[string]any{"length": 10, "units": "IN"}}
		},
		"empty package detail": func(request map[string]any) { request["packageDetails"] = []any{map[string]any{}} },
		"empty special services": func(request map[string]any) {
			request["packageDetails"] = []any{map[string]any{"packageSpecialServices": map[string]any{}}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := base()
			mutate(request)
			if err := ValidatePickupAvailabilityRequest(request); err == nil {
				t.Fatal("malformed nested availability request was accepted")
			}
		})
	}
}
