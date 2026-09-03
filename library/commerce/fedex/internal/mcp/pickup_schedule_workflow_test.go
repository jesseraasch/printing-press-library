// Copyright 2026 Trevin Chow and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/mvanhorn/printing-press-library/library/commerce/fedex/internal/store"
	"github.com/mvanhorn/printing-press-library/library/commerce/fedex/internal/workflow"
)

func TestSchedulePickupBindsPreflightAndPersistsLedger(t *testing.T) {
	availabilityCalls, scheduleCalls := 0, 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case workflow.PickupAvailabilityPath:
			availabilityCalls++
			_, _ = w.Write([]byte(`{"output":{"available":true,"cutoffTime":"17:00","accessTime":"09:00"}}`))
		case "/pickup/v1/pickups":
			scheduleCalls++
			_, _ = w.Write([]byte(`{"transactionId":"pickup-tx","output":{"pickupConfirmationCode":"PU123","scheduledDate":"2026-09-03","location":"GROUND"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("FEDEX_DATA_DIR", filepath.Join(t.TempDir(), "fedex"))
	setMCPTestAuth(t, api.URL)
	s := server.NewMCPServer("fedex-test", "test")
	RegisterTools(s)
	tool := s.ListTools()["schedule_pickup"]
	request := validMCPSchedulePickupRequest()
	address := request["originDetail"].(map[string]any)["pickupLocation"].(map[string]any)["address"]
	availability := map[string]any{
		"associatedAccountNumber": map[string]any{"value": "123456789"},
		"carriers":                []any{"FDXG"},
		"dispatchDate":            "2026-09-03",
		"packageReadyTime":        "09:00",
		"customerCloseTime":       "17:00:00",
		"pickupAddress":           address,
	}
	preview, err := tool.Handler(context.Background(), toolRequest(map[string]any{"request": request, "availability_request": availability}))
	if err != nil || preview == nil || preview.IsError {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	if availabilityCalls != 1 || scheduleCalls != 0 {
		t.Fatalf("availability=%d schedule=%d", availabilityCalls, scheduleCalls)
	}
	var pending struct {
		OperationID        string `json:"operation_id"`
		ConfirmationDigest string `json:"confirmation_digest"`
	}
	if err := json.Unmarshal([]byte(toolResultText(t, preview)), &pending); err != nil {
		t.Fatal(err)
	}
	confirmed, err := tool.Handler(context.Background(), toolRequest(map[string]any{
		"request":              request,
		"availability_request": availability,
		"confirm":              true,
		"operation_id":         pending.OperationID,
		"confirmation_digest":  pending.ConfirmationDigest,
	}))
	if err != nil || confirmed == nil || confirmed.IsError {
		t.Fatalf("confirmed=%#v err=%v", confirmed, err)
	}
	if availabilityCalls != 1 || scheduleCalls != 1 {
		t.Fatalf("availability=%d schedule=%d", availabilityCalls, scheduleCalls)
	}
	ledger, err := store.Open(store.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	pickup, err := ledger.GetPickupByOperationID(context.Background(), pending.OperationID)
	_ = ledger.Close()
	if err != nil || pickup == nil || pickup.Status != "scheduled" || pickup.ConfirmationNumber != "PU123" || pickup.PreflightStatus != "verified" || pickup.CutoffTime != "17:00" || pickup.AccessStartTime != "09:00" {
		t.Fatalf("pickup=%+v err=%v", pickup, err)
	}
}
