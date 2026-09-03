// Copyright 2026 Trevin Chow and contributors. Licensed under Apache-2.0. See LICENSE.

package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const PickupAvailabilityPath = "/pickup/v1/pickups/availabilities"

type pickupAvailabilityClient interface {
	Post(path string, body any) (json.RawMessage, int, error)
}

// PickupPreflight is approval-bound context for a pickup creation. Context may
// be hashed with the mutation but must not be sent to FedEx's creation endpoint.
type PickupPreflight struct {
	Context        any
	Status         string
	OverrideReason string
	Window         string
	CutoffTime     string
	AccessStart    string
}

// ValidatePickupAvailabilityBinding ensures that a successful availability
// response applies to the exact pickup about to be created.
func ValidatePickupAvailabilityBinding(scheduleRequest, availabilityRequest map[string]any) error {
	if err := ValidateRequest(ActionSchedulePickup, scheduleRequest); err != nil {
		return err
	}
	account := strings.TrimSpace(nestedString(availabilityRequest, "associatedAccountNumber", "value"))
	wantAccount := strings.TrimSpace(nestedString(scheduleRequest, "associatedAccountNumber", "value"))
	if account == "" || account != wantAccount {
		return fmt.Errorf("availability associatedAccountNumber.value must match the pickup request")
	}
	carrier := strings.TrimSpace(stringField(scheduleRequest, "carrierCode"))
	carriers := stringList(availabilityRequest["carriers"])
	if len(carriers) != 1 || carriers[0] != carrier {
		return fmt.Errorf("availability carriers must contain only pickup carrierCode %s", carrier)
	}
	ready := strings.TrimSpace(nestedString(scheduleRequest, "originDetail", "readyDateTimestamp"))
	readyTime, err := time.Parse(time.RFC3339, ready)
	if err != nil {
		return fmt.Errorf("originDetail.readyDateTimestamp must be RFC3339 for availability binding: %w", err)
	}
	if strings.TrimSpace(stringField(availabilityRequest, "dispatchDate")) != readyTime.Format("2006-01-02") {
		return fmt.Errorf("availability dispatchDate must match the pickup ready date")
	}
	packageReady := strings.TrimSpace(stringField(availabilityRequest, "packageReadyTime"))
	if packageReady == "" || !strings.HasPrefix(readyTime.Format("15:04:05"), packageReady) {
		return fmt.Errorf("availability packageReadyTime must match the pickup ready time")
	}
	wantClose := strings.TrimSpace(nestedString(scheduleRequest, "originDetail", "customerCloseTime"))
	if strings.TrimSpace(stringField(availabilityRequest, "customerCloseTime")) != wantClose {
		return fmt.Errorf("availability customerCloseTime must match the pickup request")
	}
	availabilityAddress, ok := objectAt(availabilityRequest, "pickupAddress")
	if !ok {
		return fmt.Errorf("availability pickupAddress is required")
	}
	pickupAddress, ok := objectAt(scheduleRequest, "originDetail", "pickupLocation", "address")
	if !ok {
		return fmt.Errorf("originDetail.pickupLocation.address is required")
	}
	left, _ := json.Marshal(availabilityAddress)
	right, _ := json.Marshal(pickupAddress)
	if !bytes.Equal(left, right) {
		return fmt.Errorf("availability pickupAddress must match originDetail.pickupLocation.address")
	}
	return nil
}

// PreparePickupPreflight requires either a successful availability request or
// a documented override. Confirmation calls re-bind the same context but do not
// repeat the preflight network request that was completed during preview.
func PreparePickupPreflight(client pickupAvailabilityClient, confirming bool, availabilityRequest map[string]any, overrideReason string) (PickupPreflight, error) {
	overrideReason = strings.TrimSpace(overrideReason)
	if len(availabilityRequest) > 0 && overrideReason != "" {
		return PickupPreflight{}, fmt.Errorf("use either an availability request or an override reason, not both")
	}
	if len(availabilityRequest) == 0 && overrideReason == "" {
		return PickupPreflight{}, fmt.Errorf("pickup scheduling requires an availability request or a documented override reason")
	}
	if overrideReason != "" {
		if len(overrideReason) < 10 {
			return PickupPreflight{}, fmt.Errorf("availability override reason must contain at least 10 characters")
		}
		return PickupPreflight{Context: map[string]any{"pickup_preflight": "overridden", "reason": overrideReason}, Status: "overridden", OverrideReason: overrideReason}, nil
	}
	result := PickupPreflight{Context: map[string]any{"pickup_preflight": "verified", "request": availabilityRequest}, Status: "verified", Window: "verified during preview"}
	if confirming {
		return result, nil
	}
	data, _, err := client.Post(PickupAvailabilityPath, availabilityRequest)
	if err != nil {
		return PickupPreflight{}, fmt.Errorf("pickup availability preflight: %w", err)
	}
	available, known, evidenceErr := pickupAvailable(data)
	if evidenceErr != nil {
		return PickupPreflight{}, evidenceErr
	}
	if known && !available {
		return PickupPreflight{}, fmt.Errorf("FedEx reported that pickup is unavailable for the requested window")
	}
	result.CutoffTime, result.AccessStart, err = pickupWindowFields(data)
	if err != nil {
		return PickupPreflight{}, err
	}
	if !known && result.CutoffTime == "" && result.AccessStart == "" {
		return PickupPreflight{}, fmt.Errorf("FedEx availability response contained no positive availability or pickup-window evidence")
	}
	parts := make([]string, 0, 2)
	if result.CutoffTime != "" {
		parts = append(parts, "cutoff="+result.CutoffTime)
	}
	if result.AccessStart != "" {
		parts = append(parts, "access="+result.AccessStart)
	}
	if len(parts) > 0 {
		result.Window = strings.Join(parts, ", ")
	}
	return result, nil
}

func pickupAvailable(data []byte) (bool, bool, error) {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return false, false, fmt.Errorf("pickup availability response is not valid JSON")
	}
	var seenTrue, seenFalse bool
	walkJSON(value, func(key string, child any) {
		switch strings.ToLower(key) {
		case "available", "pickupavailable", "isavailable":
			if flag, ok := child.(bool); ok {
				seenTrue = seenTrue || flag
				seenFalse = seenFalse || !flag
			}
		}
	})
	if seenTrue && seenFalse {
		return false, false, fmt.Errorf("pickup availability response contains conflicting availability results")
	}
	return seenTrue, seenTrue || seenFalse, nil
}

func pickupWindowFields(data []byte) (string, string, error) {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return "", "", fmt.Errorf("pickup availability response is not valid JSON")
	}
	cutoffs := map[string]struct{}{}
	accesses := map[string]struct{}{}
	walkJSON(value, func(key string, child any) {
		text, ok := child.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return
		}
		switch strings.ToLower(key) {
		case "cutofftime", "cutoff":
			cutoffs[strings.TrimSpace(text)] = struct{}{}
		case "accesstime", "accessstarttime":
			accesses[strings.TrimSpace(text)] = struct{}{}
		}
	})
	if len(cutoffs) > 1 || len(accesses) > 1 {
		return "", "", fmt.Errorf("pickup availability response contains multiple unmatched pickup windows")
	}
	return onlyString(cutoffs), onlyString(accesses), nil
}

func walkJSON(value any, visit func(string, any)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			visit(key, child)
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkJSON(child, visit)
		}
	}
}

func nestedString(value map[string]any, path ...string) string {
	current := any(value)
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	text, _ := current.(string)
	return text
}

func objectAt(value map[string]any, path ...string) (map[string]any, bool) {
	current := any(value)
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current = object[key]
	}
	object, ok := current.(map[string]any)
	return object, ok
}

func stringList(value any) []string {
	var result []string
	switch values := value.(type) {
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, strings.TrimSpace(text))
			}
		}
	case []string:
		for _, value := range values {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func onlyString(values map[string]struct{}) string {
	for value := range values {
		return value
	}
	return ""
}
