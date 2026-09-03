// Copyright 2026 Trevin Chow and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

func validateNarrowReadRequest(action string, body map[string]any) error {
	requireObject := func(name string) (map[string]any, error) {
		value, ok := body[name].(map[string]any)
		if !ok || len(value) == 0 {
			return nil, fmt.Errorf("%s must be a nonempty object", name)
		}
		return value, nil
	}
	requireString := func(name string) error {
		value, ok := body[name].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must be a nonempty string", name)
		}
		return nil
	}
	requireAccount := func(name string) error {
		account, err := requireObject(name)
		if err != nil {
			return err
		}
		value, ok := account["value"].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("account number value must be a nonempty string")
		}
		return nil
	}

	switch action {
	case "get_rates":
		if err := requireAccount("accountNumber"); err != nil {
			return err
		}
		_, err := requireObject("requestedShipment")
		return err
	case "validate_address":
		addresses, ok := body["addressesToValidate"].([]any)
		if !ok || len(addresses) == 0 || len(addresses) > 100 {
			return fmt.Errorf("addressesToValidate must contain between 1 and 100 addresses")
		}
		for i, value := range addresses {
			entry, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("addressesToValidate[%d] must be an object", i)
			}
			address, ok := entry["address"].(map[string]any)
			if !ok || len(address) == 0 {
				return fmt.Errorf("addressesToValidate[%d].address must be a nonempty object", i)
			}
		}
		return nil
	case "validate_shipment":
		if err := requireAccount("accountNumber"); err != nil {
			return err
		}
		shipment, err := requireObject("requestedShipment")
		if err != nil {
			return err
		}
		packages, ok := shipment["requestedPackageLineItems"].([]any)
		if !ok || len(packages) != 1 {
			return fmt.Errorf("requestedShipment.requestedPackageLineItems must contain exactly one package")
		}
		pkg, ok := packages[0].(map[string]any)
		if !ok {
			return fmt.Errorf("requestedShipment.requestedPackageLineItems[0] must be an object")
		}
		if count, present := pkg["groupPackageCount"]; present && !numericOne(count) {
			return fmt.Errorf("requestedShipment.requestedPackageLineItems[0].groupPackageCount must be the integer 1 when provided")
		}
		return nil
	case "pickup_availability":
		if _, err := requireObject("pickupAddress"); err != nil {
			return err
		}
		if err := requireAccount("associatedAccountNumber"); err != nil {
			return err
		}
		for _, name := range []string{"dispatchDate", "packageReadyTime", "customerCloseTime"} {
			if err := requireString(name); err != nil {
				return err
			}
		}
		carriers, ok := body["carriers"].([]any)
		if !ok || len(carriers) == 0 {
			return fmt.Errorf("carriers must contain at least one carrier code")
		}
		return nil
	default:
		return fmt.Errorf("unsupported read operation %q", action)
	}
}

func numericOne(value any) bool {
	switch typed := value.(type) {
	case int:
		return typed == 1
	case int32:
		return typed == 1
	case int64:
		return typed == 1
	case float32:
		return typed == 1
	case float64:
		return typed == 1
	case json.Number:
		integer, err := typed.Int64()
		return err == nil && integer == 1
	default:
		return false
	}
}

func minimizeNarrowReadResponse(action string, data []byte) (any, error) {
	switch action {
	case "get_rates":
		return minimizeRateResponse(data)
	case "validate_address":
		return minimizeAddressResponse(data)
	case "validate_shipment":
		if !json.Valid(data) {
			return nil, fmt.Errorf("FedEx shipment validation response was not valid JSON")
		}
		return map[string]any{"valid": true}, nil
	case "pickup_availability":
		return minimizePickupAvailabilityResponse(data)
	default:
		return nil, fmt.Errorf("unsupported read operation %q", action)
	}
}

func minimizeRateResponse(data []byte) (any, error) {
	var response struct {
		Output struct {
			RateReplyDetails []struct {
				ServiceType       string `json:"serviceType"`
				OperationalDetail struct {
					TransitTime string `json:"transitTime"`
					DeliveryDay string `json:"deliveryDay"`
				} `json:"operationalDetail"`
				RatedShipmentDetails []struct {
					RateType            string  `json:"rateType"`
					TotalNetCharge      float64 `json:"totalNetCharge"`
					TotalBaseCharge     float64 `json:"totalBaseCharge"`
					TotalNetFedExCharge float64 `json:"totalNetFedExCharge"`
					Currency            string  `json:"currency"`
					ShipmentRateDetail  struct {
						RateType       string  `json:"rateType"`
						TotalNetCharge float64 `json:"totalNetCharge"`
						Currency       string  `json:"currency"`
					} `json:"shipmentRateDetail"`
				} `json:"ratedShipmentDetails"`
			} `json:"rateReplyDetails"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("FedEx rate response was not valid JSON: %w", err)
	}
	rates := make([]map[string]any, 0)
	for _, detail := range response.Output.RateReplyDetails {
		for _, rated := range detail.RatedShipmentDetails {
			rateType := rated.RateType
			if rateType == "" {
				rateType = rated.ShipmentRateDetail.RateType
			}
			netCharge := rated.TotalNetCharge
			if netCharge == 0 {
				netCharge = rated.ShipmentRateDetail.TotalNetCharge
			}
			currency := rated.Currency
			if currency == "" {
				currency = rated.ShipmentRateDetail.Currency
			}
			rates = append(rates, map[string]any{
				"service_type":           detail.ServiceType,
				"rate_type":              rateType,
				"total_net_charge":       netCharge,
				"total_base_charge":      rated.TotalBaseCharge,
				"total_net_fedex_charge": rated.TotalNetFedExCharge,
				"currency":               currency,
				"transit_time":           detail.OperationalDetail.TransitTime,
				"delivery_day":           detail.OperationalDetail.DeliveryDay,
			})
		}
	}
	return map[string]any{"rates": rates}, nil
}

func minimizeAddressResponse(data []byte) (any, error) {
	var response struct {
		Output struct {
			ResolvedAddresses []struct {
				Classification      string   `json:"classification"`
				StreetLines         []string `json:"streetLines"`
				StreetLinesToken    []string `json:"streetLinesToken"`
				City                string   `json:"city"`
				StateOrProvinceCode string   `json:"stateOrProvinceCode"`
				PostalCode          string   `json:"postalCode"`
				CountryCode         string   `json:"countryCode"`
			} `json:"resolvedAddresses"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("FedEx address response was not valid JSON: %w", err)
	}
	addresses := make([]map[string]any, 0, len(response.Output.ResolvedAddresses))
	for _, resolved := range response.Output.ResolvedAddresses {
		streetLines := resolved.StreetLines
		if len(streetLines) == 0 {
			streetLines = resolved.StreetLinesToken
		}
		addresses = append(addresses, map[string]any{
			"classification":         resolved.Classification,
			"street_lines":           streetLines,
			"city":                   resolved.City,
			"state_or_province_code": resolved.StateOrProvinceCode,
			"postal_code":            resolved.PostalCode,
			"country_code":           resolved.CountryCode,
		})
	}
	return map[string]any{"resolved_addresses": addresses}, nil
}

func minimizePickupAvailabilityResponse(data []byte) (any, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("FedEx pickup availability response was not valid JSON: %w", err)
	}
	availableValues := map[bool]struct{}{}
	cutoffs := map[string]struct{}{}
	accesses := map[string]struct{}{}
	walkReadResponse(value, func(key string, child any) {
		switch strings.ToLower(key) {
		case "available", "pickupavailable", "isavailable":
			if available, ok := child.(bool); ok {
				availableValues[available] = struct{}{}
			}
		case "cutofftime", "cutoff":
			if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
				cutoffs[strings.TrimSpace(text)] = struct{}{}
			}
		case "accesstime", "accessstarttime":
			if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
				accesses[strings.TrimSpace(text)] = struct{}{}
			}
		}
	})
	if len(availableValues) > 1 || len(cutoffs) > 1 || len(accesses) > 1 {
		return nil, fmt.Errorf("FedEx pickup availability response contained conflicting results")
	}
	result := map[string]any{"availability_known": len(availableValues) == 1}
	for available := range availableValues {
		result["available"] = available
	}
	for cutoff := range cutoffs {
		result["cutoff_time"] = cutoff
	}
	for access := range accesses {
		result["access_time"] = access
	}
	return result, nil
}

func walkReadResponse(value any, visit func(string, any)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			visit(key, child)
			walkReadResponse(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkReadResponse(child, visit)
		}
	}
}
