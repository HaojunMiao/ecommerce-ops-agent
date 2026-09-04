package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HaojunMiao/ecommerce-ops-agent/projects/crossborder/internal/service"
)

func TestGetOrderTool(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/tools/get_order", strings.NewReader(`{"order_id":"TTS-20260801-1001"}`))
	rec := httptest.NewRecorder()
	New(service.NewSeeded()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "awaiting_shipment") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetInventoryIncludesTransferEvidence(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/tools/get_inventory", strings.NewReader(`{"sku":"SKU-BLACK-M-01"}`))
	rec := httptest.NewRecorder()
	New(service.NewSeeded()).ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"WH-US-SFO"`) || !strings.Contains(body, `"estimated_hours":6`) || !strings.Contains(body, `"observed_at"`) || !strings.Contains(body, `"estimated_arrival"`) {
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
}

func TestChangeFulfillmentWarehouseTool(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/tools/change_fulfillment_warehouse", strings.NewReader(`{
		"order_id":"TTS-20260801-1003",
		"to_warehouse":"WH-US-BOS",
		"reason":"transfer misses ship_by",
		"idempotency_key":"reroute-http-1"
	}`))
	rec := httptest.NewRecorder()
	New(service.NewSeeded()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"changed"`) || !strings.Contains(rec.Body.String(), `"id":"FW-0001"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
