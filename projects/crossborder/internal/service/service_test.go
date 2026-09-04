package service

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/projects/crossborder/internal/domain"
)

func TestSeededUrgentFulfillmentScenarioHasEnoughEvidence(t *testing.T) {
	svc := NewSeeded()
	order, err := svc.GetOrder("TTS-20260801-1001")
	if err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(order.ShipBy)
	if order.FulfillmentWH != "WH-US-LAX" || remaining < 7*time.Hour || remaining > 8*time.Hour {
		t.Fatalf("unexpected order: %#v remaining=%s", order, remaining)
	}
	available := map[string]int{}
	for _, balance := range svc.Inventory("SKU-BLACK-M-01") {
		available[balance.WarehouseID] = balance.Available
	}
	if available["WH-US-LAX"] != 0 || available["WH-US-SFO"] != 18 {
		t.Fatalf("unexpected inventory: %#v", available)
	}
	lanes := svc.TransferLanes("SKU-BLACK-M-01", time.Now().UTC())
	if len(lanes) != 1 || lanes[0].FromWarehouse != "WH-US-SFO" || lanes[0].ToWarehouse != "WH-US-LAX" || lanes[0].EstimatedHours != 6 {
		t.Fatalf("unexpected transfer lanes: %#v", lanes)
	}
	if untilArrival := time.Until(lanes[0].EstimatedArrival); untilArrival < 5*time.Hour || untilArrival > 6*time.Hour {
		t.Fatalf("unexpected estimated arrival: %#v", lanes[0])
	}
	options, err := svc.ShippingOptions(order.ID, "")
	if err != nil || len(options) == 0 {
		t.Fatalf("shipping options: %#v err=%v", options, err)
	}
	for _, option := range options {
		if option.WarehouseID != order.FulfillmentWH || !option.SLAEligible {
			t.Fatalf("option does not match order: %#v", option)
		}
	}
}

func TestInventoryTransferIsValidatedAndIdempotent(t *testing.T) {
	svc := NewSeeded()
	req := TransferRequest{SKU: "SKU-BLACK-M-01", FromWarehouse: "WH-US-SFO", ToWarehouse: "WH-US-LAX", Quantity: 2, IdempotencyKey: "transfer-1"}
	first, err := svc.CreateTransfer(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateTransfer(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent replay created %q and %q", first.ID, second.ID)
	}
	balances := svc.Inventory(req.SKU)
	available, inbound := map[string]int{}, map[string]int{}
	for _, balance := range balances {
		available[balance.WarehouseID] = balance.Available
		inbound[balance.WarehouseID] = balance.Inbound
	}
	if available["WH-US-SFO"] != 16 || available["WH-US-LAX"] != 0 || inbound["WH-US-LAX"] != 2 {
		t.Fatalf("unexpected balances: available=%#v inbound=%#v", available, inbound)
	}
	if first.Status != "in_transit" || first.EstimatedArrival.IsZero() {
		t.Fatalf("unexpected transfer: %#v", first)
	}
}

func TestChangeFulfillmentWarehouseUsesStockedSLAEligibleCandidate(t *testing.T) {
	svc := NewSeeded()
	order, err := svc.GetOrder("TTS-20260801-1003")
	if err != nil {
		t.Fatal(err)
	}
	lanes := svc.TransferLanes("SKU-BLUE-S-03", time.Now().UTC())
	if len(lanes) != 1 || lanes[0].EstimatedHours != 12 || time.Until(order.ShipBy) >= 12*time.Hour {
		t.Fatalf("expected transfer to miss ship_by: order=%#v lanes=%#v", order, lanes)
	}
	if !lanes[0].EstimatedArrival.After(order.ShipBy) {
		t.Fatalf("estimated arrival must be later than ship_by: order=%#v lane=%#v", order, lanes[0])
	}
	options, err := svc.ShippingOptions(order.ID, "WH-US-BOS")
	if err != nil || len(options) == 0 || !options[0].SLAEligible {
		t.Fatalf("expected an SLA-eligible BOS option: %#v err=%v", options, err)
	}
	req := ChangeFulfillmentWarehouseRequest{
		OrderID: order.ID, ToWarehouse: "WH-US-BOS", Reason: "transfer cannot arrive before ship_by",
		IdempotencyKey: "reroute-1",
	}
	first, err := svc.ChangeFulfillmentWarehouse(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ChangeFulfillmentWarehouse(req)
	if err != nil || second.ID != first.ID {
		t.Fatalf("idempotent replay: first=%#v second=%#v err=%v", first, second, err)
	}
	if first.Status != "changed" || first.FromWarehouse != "WH-US-LAX" || first.ToWarehouse != "WH-US-BOS" {
		t.Fatalf("unexpected change: %#v", first)
	}
	updated, _ := svc.GetOrder(order.ID)
	if updated.FulfillmentWH != "WH-US-BOS" {
		t.Fatalf("fulfillment warehouse = %q", updated.FulfillmentWH)
	}
	for _, balance := range svc.Inventory("SKU-BLUE-S-03") {
		if balance.WarehouseID == "WH-US-BOS" && (balance.Available != 11 || balance.Reserved != 1) {
			t.Fatalf("unexpected BOS balance: %#v", balance)
		}
	}
}

func TestRefundEnforcesOrderStateAndAmount(t *testing.T) {
	svc := NewSeeded()
	_, err := svc.ApproveRefund(RefundRequest{OrderID: "TTS-20260801-1001", Amount: 129.99, IdempotencyKey: "refund-closed"})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("want order without a buyer cancellation request to be rejected, got %v", err)
	}
	_, err = svc.ApproveRefund(RefundRequest{OrderID: "TTS-20260801-1002", Amount: 999, IdempotencyKey: "refund-invalid"})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("want invalid transition, got %v", err)
	}
	refund, err := svc.ApproveRefund(RefundRequest{OrderID: "TTS-20260801-1002", Amount: 59.90, Reason: "buyer cancellation", IdempotencyKey: "refund-1"})
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "approved" {
		t.Fatalf("unexpected refund: %#v", refund)
	}
	order, _ := svc.GetOrder("TTS-20260801-1002")
	if order.Status != domain.OrderCancelled {
		t.Fatalf("unexpected order status %q", order.Status)
	}
}

func TestReconciliationUsesStatementDifference(t *testing.T) {
	svc := NewSeeded()
	item, err := svc.CreateReconciliation(ReconciliationRequest{StatementID: "STMT-2026-31", Reason: "platform shipping fee duplicated", IdempotencyKey: "rc-1"})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(item.Difference-11.52) > 0.0001 {
		t.Fatalf("want 11.52, got %.2f", item.Difference)
	}
}
