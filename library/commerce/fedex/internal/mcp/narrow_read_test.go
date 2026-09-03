// Copyright 2026 Trevin Chow and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMinimizeNarrowReadResponseAllowListsOperationalFields(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		response  string
		want      []string
		forbidden []string
	}{
		{
			name:      "rates",
			action:    "get_rates",
			response:  `{"output":{"rateReplyDetails":[{"serviceType":"FEDEX_GROUND","operationalDetail":{"transitTime":"TWO_DAYS","deliveryDay":"FRI"},"ratedShipmentDetails":[{"rateType":"PAYOR_ACCOUNT_PACKAGE","totalNetCharge":12.34,"totalBaseCharge":15,"currency":"USD"}]}]},"messages":[{"message":"unrelated"}],"requestEcho":{"name":"Recipient"}}`,
			want:      []string{`"service_type":"FEDEX_GROUND"`, `"total_net_charge":12.34`, `"transit_time":"TWO_DAYS"`},
			forbidden: []string{"unrelated", "requestEcho", "Recipient"},
		},
		{
			name:      "address",
			action:    "validate_address",
			response:  `{"output":{"resolvedAddresses":[{"classification":"BUSINESS","streetLines":["500 Main St"],"city":"Denver","stateOrProvinceCode":"CO","postalCode":"80202","countryCode":"US","customerName":"Recipient"}]},"messages":[{"message":"unrelated"}]}`,
			want:      []string{`"classification":"BUSINESS"`, `"postal_code":"80202"`, `"street_lines":["500 Main St"]`},
			forbidden: []string{"customerName", "Recipient", "unrelated"},
		},
		{
			name:      "shipment validation",
			action:    "validate_shipment",
			response:  `{"transactionId":"tx-secret","output":{"alerts":[{"message":"unrelated"}],"requestEcho":{"name":"Recipient"}}}`,
			want:      []string{`"valid":true`},
			forbidden: []string{"tx-secret", "unrelated", "Recipient"},
		},
		{
			name:      "pickup availability",
			action:    "pickup_availability",
			response:  `{"output":{"available":true,"cutoffTime":"17:00","accessTime":"09:00","pickupAddress":{"streetLines":["500 Main St"]}},"messages":[{"message":"unrelated"}]}`,
			want:      []string{`"availability_known":true`, `"available":true`, `"cutoff_time":"17:00"`, `"access_time":"09:00"`},
			forbidden: []string{"500 Main St", "unrelated", "messages"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := minimizeNarrowReadResponse(test.action, []byte(test.response))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			text := string(encoded)
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Errorf("result %s missing %s", text, want)
				}
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(text, forbidden) {
					t.Errorf("result %s exposed %s", text, forbidden)
				}
			}
		})
	}
}

func TestValidateNarrowReadRequestRejectsIncompleteAndMultiPieceRequests(t *testing.T) {
	if err := validateNarrowReadRequest("get_rates", map[string]any{
		"accountNumber": map[string]any{"value": "123"},
	}); err == nil {
		t.Fatal("get_rates accepted a request without requestedShipment")
	}
	if err := validateNarrowReadRequest("validate_address", map[string]any{
		"addressesToValidate": []any{},
	}); err == nil {
		t.Fatal("validate_address accepted an empty address list")
	}
	if err := validateNarrowReadRequest("validate_shipment", map[string]any{
		"accountNumber": map[string]any{"value": "123"},
		"requestedShipment": map[string]any{
			"requestedPackageLineItems": []any{map[string]any{}, map[string]any{}},
		},
	}); err == nil {
		t.Fatal("validate_shipment accepted multiple package line items")
	}
	if err := validateNarrowReadRequest("validate_shipment", map[string]any{
		"accountNumber": map[string]any{"value": "123"},
		"requestedShipment": map[string]any{
			"requestedPackageLineItems": []any{map[string]any{"groupPackageCount": 2}},
		},
	}); err == nil {
		t.Fatal("validate_shipment accepted a grouped multi-piece package")
	}
	if err := validateNarrowReadRequest("pickup_availability", map[string]any{
		"pickupAddress":           map[string]any{"postalCode": "80202"},
		"associatedAccountNumber": map[string]any{"value": "123"},
		"dispatchDate":            "2026-09-03",
		"packageReadyTime":        "09:00",
		"customerCloseTime":       "17:00",
	}); err == nil {
		t.Fatal("pickup_availability accepted a request without carriers")
	}
}
