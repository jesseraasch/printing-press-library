// Copyright 2026 Trevin Chow and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
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
			response:  `{"transactionId":"tx-secret","output":{"alerts":[{"code":"SHIPMENT.VALIDATION.SUCCESS","alertType":"NOTE","message":"unrelated"}],"requestEcho":{"name":"Recipient"}}}`,
			want:      []string{`"valid":true`, `"code":"SHIPMENT.VALIDATION.SUCCESS"`, `"alert_type":"NOTE"`},
			forbidden: []string{"tx-secret", "unrelated", "Recipient"},
		},
		{
			name:      "pickup availability",
			action:    "pickup_availability",
			response:  `{"output":{"options":[{"carrier":"FDXG","available":true,"pickupDate":"2026-09-03","cutOffTime":"17:00","accessTime":{"hours":1,"minutes":30},"residentialAvailable":true,"countryRelationship":"DOMESTIC","scheduleDay":"THU","defaultReadyTime":"09:00","defaultLatestTimeOptions":"17:00","pickupAddress":{"streetLines":["500 Main St"]}}]},"messages":[{"message":"unrelated"}]}`,
			want:      []string{`"carrier":"FDXG"`, `"available":true`, `"pickup_date":"2026-09-03"`, `"cutoff_time":"17:00"`, `"access_time":{"hours":1,"minutes":30}`},
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

func TestShipmentValidationRequiresExplicitFedExEvidence(t *testing.T) {
	for name, response := range map[string]string{
		"message only": `{"output":{"alerts":[{"message":"looks fine"}]}}`,
		"warning only": `{"output":{"alerts":[{"code":"SHIPMENT.WARNING","alertType":"WARNING"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := minimizeShipmentValidationResponse([]byte(response)); err == nil {
				t.Fatal("response without explicit validation evidence was accepted")
			}
		})
	}
	result, err := minimizeShipmentValidationResponse([]byte(`{"output":{"alerts":[{"code":"SHIPMENT.ERROR","alertType":"ERROR","message":"recipient PII"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), `"valid":false`) || strings.Contains(string(encoded), "recipient PII") {
		t.Fatalf("unexpected minimized rejection: %s", encoded)
	}
}

func TestPickupAvailabilityRetainsDistinctOfficialOptions(t *testing.T) {
	response := []byte(`{"output":{"options":[{"carrier":"FDXG","available":true,"pickupDate":"2026-09-03","cutOffTime":"17:00","accessTime":{"hours":1,"minutes":0}},{"carrier":"FDXE","available":false,"pickupDate":"2026-09-04","cutOffTime":"15:00","accessTime":{"hours":2,"minutes":30}}]}}`)
	result, err := minimizePickupAvailabilityResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	for _, want := range []string{`"carrier":"FDXG"`, `"carrier":"FDXE"`, `"pickup_date":"2026-09-03"`, `"pickup_date":"2026-09-04"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("minimized options %s missing %s", encoded, want)
		}
	}
}

type malformedReadRequest struct {
	name   string
	action string
	body   map[string]any
}

func malformedReadRequests() []malformedReadRequest {
	return []malformedReadRequest{
		{"rates wrong account field", "get_rates", map[string]any{"accountNumber": map[string]any{"associatedAccountNumber": "123"}, "requestedShipment": map[string]any{"arbitrary": true}}},
		{"rates shallow shipment", "get_rates", map[string]any{"accountNumber": map[string]any{"value": "123"}, "requestedShipment": map[string]any{"arbitrary": true}}},
		{"address missing street", "validate_address", map[string]any{"addressesToValidate": []any{map[string]any{"city": "Denver", "postalCode": "80202", "countryCode": "US"}}}},
		{"shipment missing parties", "validate_shipment", map[string]any{"accountNumber": map[string]any{"value": "123"}, "requestedShipment": map[string]any{"serviceType": "FEDEX_GROUND", "packagingType": "YOUR_PACKAGING", "requestedPackageLineItems": []any{map[string]any{"weight": map[string]any{"units": "LB", "value": 2}}}}}},
		{"shipment grouped package", "validate_shipment", map[string]any{"accountNumber": map[string]any{"value": "123"}, "requestedShipment": map[string]any{"shipper": testReadParty(), "recipients": []any{testReadParty()}, "serviceType": "FEDEX_GROUND", "packagingType": "YOUR_PACKAGING", "requestedPackageLineItems": []any{map[string]any{"groupPackageCount": 2, "weight": map[string]any{"units": "LB", "value": 2}}}}}},
		{"shipment wrong sequence", "validate_shipment", map[string]any{"accountNumber": map[string]any{"value": "123"}, "requestedShipment": map[string]any{"shipper": testReadParty(), "recipients": []any{testReadParty()}, "serviceType": "FEDEX_GROUND", "packagingType": "YOUR_PACKAGING", "requestedPackageLineItems": []any{map[string]any{"sequenceNumber": 2, "weight": map[string]any{"units": "LB", "value": 2}}}}}},
		{"pickup numeric carrier", "pickup_availability", map[string]any{"pickupAddress": testReadAddress(), "pickupRequestType": []any{"SAME_DAY"}, "carriers": []any{1}, "countryRelationship": "DOMESTIC"}},
		{"pickup nonobject package detail", "pickup_availability", map[string]any{"pickupAddress": testReadAddress(), "pickupRequestType": []any{"SAME_DAY"}, "carriers": []any{"FDXG"}, "countryRelationship": "DOMESTIC", "packageDetails": []any{"invalid"}}},
		{"pickup wrong account shape", "pickup_availability", map[string]any{"pickupAddress": testReadAddress(), "pickupRequestType": []any{"SAME_DAY"}, "carriers": []any{"FDXG"}, "countryRelationship": "DOMESTIC", "associatedAccountNumber": map[string]any{"value": "123"}}},
		{"pickup missing request type", "pickup_availability", map[string]any{"pickupAddress": testReadAddress(), "carriers": []any{"FDXG"}, "countryRelationship": "DOMESTIC"}},
	}
}

func TestValidateNarrowReadRequestRejectsMalformedNestedRequests(t *testing.T) {
	for _, test := range malformedReadRequests() {
		t.Run(test.name, func(t *testing.T) {
			if err := validateNarrowReadRequest(test.action, test.body); err == nil {
				t.Fatalf("%s accepted malformed request", test.action)
			}
		})
	}
}

func TestValidateNarrowReadRequestAcceptsWellFormedRequests(t *testing.T) {
	for action, body := range testReadRequests() {
		t.Run(action, func(t *testing.T) {
			if err := validateNarrowReadRequest(action, body); err != nil {
				t.Fatalf("well-formed request rejected: %v", err)
			}
		})
	}
}

func TestMalformedNarrowReadRequestsMakeNoHTTPCalls(t *testing.T) {
	for _, test := range malformedReadRequests() {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			defer api.Close()
			setMCPTestAuth(t, api.URL)
			var operation narrowOperation
			for _, candidate := range narrowOperations {
				if candidate.action == test.action {
					operation = candidate
					break
				}
			}
			result, err := makeNarrowReadHandler(operation)(context.Background(), mcplib.CallToolRequest{Params: mcplib.CallToolParams{Arguments: map[string]any{"request": test.body}}})
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || !result.IsError {
				t.Fatalf("malformed request did not produce a tool error: %#v", result)
			}
			if got := calls.Load(); got != 0 {
				t.Fatalf("malformed request made %d HTTP calls", got)
			}
		})
	}
}

func testReadRequests() map[string]map[string]any {
	return map[string]map[string]any{
		"get_rates": {
			"accountNumber": map[string]any{"value": "123"},
			"carrierCodes":  []any{"FDXG"},
			"requestedShipment": map[string]any{
				"shipper":         map[string]any{"address": map[string]any{"postalCode": "78701", "countryCode": "US"}},
				"recipient":       map[string]any{"address": map[string]any{"postalCode": "80202", "countryCode": "US"}},
				"pickupType":      "DROPOFF_AT_FEDEX_LOCATION",
				"rateRequestType": []any{"ACCOUNT"},
				"requestedPackageLineItems": []any{map[string]any{
					"weight": map[string]any{"units": "LB", "value": 2},
				}},
			},
		},
		"validate_address": {"addressesToValidate": []any{map[string]any{"address": testReadAddress()}}},
		"validate_shipment": {
			"accountNumber": map[string]any{"value": "123"},
			"requestedShipment": map[string]any{
				"shipper":                   testReadParty(),
				"recipients":                []any{testReadParty()},
				"serviceType":               "FEDEX_GROUND",
				"packagingType":             "YOUR_PACKAGING",
				"totalPackageCount":         1,
				"requestedPackageLineItems": []any{map[string]any{"sequenceNumber": 1, "groupPackageCount": 1, "weight": map[string]any{"units": "LB", "value": 2}}},
			},
		},
		"pickup_availability": {
			"pickupAddress":           testReadAddress(),
			"pickupRequestType":       []any{"SAME_DAY"},
			"carriers":                []any{"FDXG"},
			"countryRelationship":     "DOMESTIC",
			"associatedAccountNumber": "123",
			"dispatchDate":            "2026-09-03",
			"packageReadyTime":        "09:00",
			"customerCloseTime":       "17:00",
			"packageDetails":          []any{map[string]any{"packageCount": 1, "weight": map[string]any{"units": "LB", "value": 2}}},
		},
	}
}

func testReadAddress() map[string]any {
	return map[string]any{"streetLines": []any{"1 Test Way"}, "city": "Austin", "stateOrProvinceCode": "TX", "postalCode": "78701", "countryCode": "US"}
}

func testReadParty() map[string]any {
	return map[string]any{"contact": map[string]any{"phoneNumber": "5555550100"}, "address": testReadAddress()}
}
