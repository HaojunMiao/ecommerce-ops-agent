package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/HaojunMiao/ecommerce-ops-agent/projects/crossborder/internal/domain"
)

var (
	ErrNotFound          = errors.New("business resource not found")
	ErrInvalidTransition = errors.New("invalid business state transition")
	ErrInsufficientStock = errors.New("insufficient inventory")
	ErrIdempotencyKey    = errors.New("idempotency_key is required")
)

type Service struct {
	mu               sync.RWMutex
	orders           map[string]domain.Order
	inventory        map[string]domain.InventoryBalance
	transferLanes    []domain.TransferLane
	statements       map[string]domain.SettlementStatement
	transfers        map[string]domain.InventoryTransfer
	warehouseChanges map[string]domain.FulfillmentWarehouseChange
	refunds          map[string]domain.Refund
	reconciliations  map[string]domain.ReconciliationCase
	idempotency      map[string]any
}

func NewSeeded() *Service {
	now := time.Now().UTC().Truncate(time.Second)
	return &Service{
		orders: map[string]domain.Order{
			"TTS-20260801-1001": {
				ID: "TTS-20260801-1001", ShopID: "SHOP-US-001", Market: "US",
				Currency: "USD", Amount: 129.99, Status: domain.OrderAwaitingShipment,
				FulfillmentWH: "WH-US-LAX", ShipBy: now.Add(8 * time.Hour), BuyerCancellationRequested: false,
				Items: []domain.OrderItem{{SKU: "SKU-BLACK-M-01", Quantity: 1, Price: 129.99}},
			},
			"TTS-20260801-1002": {
				ID: "TTS-20260801-1002", ShopID: "SHOP-US-001", Market: "US",
				Currency: "USD", Amount: 59.90, Status: domain.OrderAwaitingShipment,
				FulfillmentWH: "WH-US-LAX", ShipBy: now.Add(20 * time.Hour), BuyerCancellationRequested: true,
				Items: []domain.OrderItem{{SKU: "SKU-RED-L-02", Quantity: 1, Price: 59.90}},
			},
			"TTS-20260801-1003": {
				ID: "TTS-20260801-1003", ShopID: "SHOP-US-001", Market: "US",
				Currency: "USD", Amount: 89.00, Status: domain.OrderAwaitingShipment,
				FulfillmentWH: "WH-US-LAX", ShipBy: now.Add(6 * time.Hour), BuyerCancellationRequested: false,
				Items: []domain.OrderItem{{SKU: "SKU-BLUE-S-03", Quantity: 1, Price: 89.00}},
			},
		},
		inventory: map[string]domain.InventoryBalance{
			inventoryKey("WH-US-LAX", "SKU-BLACK-M-01"): {WarehouseID: "WH-US-LAX", SKU: "SKU-BLACK-M-01", Available: 0},
			inventoryKey("WH-US-SFO", "SKU-BLACK-M-01"): {WarehouseID: "WH-US-SFO", SKU: "SKU-BLACK-M-01", Available: 18},
			inventoryKey("WH-US-LAX", "SKU-RED-L-02"):   {WarehouseID: "WH-US-LAX", SKU: "SKU-RED-L-02", Available: 6},
			inventoryKey("WH-US-LAX", "SKU-BLUE-S-03"):  {WarehouseID: "WH-US-LAX", SKU: "SKU-BLUE-S-03", Available: 0},
			inventoryKey("WH-US-BOS", "SKU-BLUE-S-03"):  {WarehouseID: "WH-US-BOS", SKU: "SKU-BLUE-S-03", Available: 12},
		},
		transferLanes: []domain.TransferLane{
			{FromWarehouse: "WH-US-SFO", ToWarehouse: "WH-US-LAX", EstimatedHours: 6},
			{FromWarehouse: "WH-US-BOS", ToWarehouse: "WH-US-LAX", EstimatedHours: 12},
		},
		statements: map[string]domain.SettlementStatement{
			"STMT-2026-31": {ID: "STMT-2026-31", ExpectedAmount: 118.47, PaidAmount: 106.95, Currency: "USD", Status: "difference_detected"},
		},
		transfers: make(map[string]domain.InventoryTransfer), warehouseChanges: make(map[string]domain.FulfillmentWarehouseChange), refunds: make(map[string]domain.Refund),
		reconciliations: make(map[string]domain.ReconciliationCase), idempotency: make(map[string]any),
	}
}

func (s *Service) GetOrder(id string) (domain.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	order, ok := s.orders[id]
	if !ok {
		return domain.Order{}, ErrNotFound
	}
	return order, nil
}

func (s *Service) Inventory(sku string) []domain.InventoryBalance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []domain.InventoryBalance
	for _, balance := range s.inventory {
		if balance.SKU == sku {
			result = append(result, balance)
		}
	}
	return result
}

func (s *Service) TransferLanes(sku string, observedAt time.Time) []domain.TransferLane {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.TransferLane, 0, len(s.transferLanes))
	for _, lane := range s.transferLanes {
		if balance, ok := s.inventory[inventoryKey(lane.FromWarehouse, sku)]; ok && balance.Available > 0 {
			lane.EstimatedArrival = observedAt.Add(time.Duration(lane.EstimatedHours) * time.Hour)
			result = append(result, lane)
		}
	}
	return result
}

func (s *Service) ShippingOptions(orderID, warehouseID string) ([]domain.ShippingOption, error) {
	order, err := s.GetOrder(orderID)
	if err != nil {
		return nil, err
	}
	if warehouseID == "" {
		warehouseID = order.FulfillmentWH
	}
	switch warehouseID {
	case "WH-US-LAX":
		return []domain.ShippingOption{
			{WarehouseID: warehouseID, Provider: "USPS", Service: "Priority Mail", Cost: 10.20, Currency: "USD", DeliveryDays: 3, SLAEligible: true},
			{WarehouseID: warehouseID, Provider: "UPS", Service: "Ground", Cost: 9.40, Currency: "USD", DeliveryDays: 4, SLAEligible: true},
		}, nil
	case "WH-US-BOS":
		return []domain.ShippingOption{
			{WarehouseID: warehouseID, Provider: "FedEx", Service: "2Day", Cost: 14.50, Currency: "USD", DeliveryDays: 2, SLAEligible: true},
			{WarehouseID: warehouseID, Provider: "UPS", Service: "Ground", Cost: 11.10, Currency: "USD", DeliveryDays: 5, SLAEligible: false},
		}, nil
	default:
		return nil, ErrNotFound
	}
}

type TransferRequest struct {
	SKU, FromWarehouse, ToWarehouse, IdempotencyKey string
	Quantity                                        int
	DryRun                                          bool
}

func (s *Service) CreateTransfer(req TransferRequest) (domain.InventoryTransfer, error) {
	if req.IdempotencyKey == "" {
		return domain.InventoryTransfer{}, ErrIdempotencyKey
	}
	if req.Quantity <= 0 || req.FromWarehouse == req.ToWarehouse {
		return domain.InventoryTransfer{}, ErrInvalidTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.idempotency["transfer:"+req.IdempotencyKey]; ok {
		return cached.(domain.InventoryTransfer), nil
	}
	fromKey, toKey := inventoryKey(req.FromWarehouse, req.SKU), inventoryKey(req.ToWarehouse, req.SKU)
	lane, ok := s.findTransferLane(req.FromWarehouse, req.ToWarehouse)
	if !ok {
		return domain.InventoryTransfer{}, ErrInvalidTransition
	}
	from, ok := s.inventory[fromKey]
	if !ok {
		return domain.InventoryTransfer{}, ErrNotFound
	}
	if from.Available < req.Quantity {
		return domain.InventoryTransfer{}, ErrInsufficientStock
	}
	if req.DryRun {
		return domain.InventoryTransfer{SKU: req.SKU, FromWarehouse: req.FromWarehouse, ToWarehouse: req.ToWarehouse, Quantity: req.Quantity, Status: "validated", EstimatedArrival: time.Now().UTC().Add(time.Duration(lane.EstimatedHours) * time.Hour)}, nil
	}
	to := s.inventory[toKey]
	to.WarehouseID, to.SKU = req.ToWarehouse, req.SKU
	from.Available -= req.Quantity
	to.Inbound += req.Quantity
	s.inventory[fromKey], s.inventory[toKey] = from, to
	createdAt := time.Now().UTC()
	transfer := domain.InventoryTransfer{ID: fmt.Sprintf("TR-%04d", len(s.transfers)+1), SKU: req.SKU, FromWarehouse: req.FromWarehouse, ToWarehouse: req.ToWarehouse, Quantity: req.Quantity, Status: "in_transit", EstimatedArrival: createdAt.Add(time.Duration(lane.EstimatedHours) * time.Hour), CreatedAt: createdAt}
	s.transfers[transfer.ID] = transfer
	s.idempotency["transfer:"+req.IdempotencyKey] = transfer
	return transfer, nil
}

func (s *Service) findTransferLane(from, to string) (domain.TransferLane, bool) {
	for _, lane := range s.transferLanes {
		if lane.FromWarehouse == from && lane.ToWarehouse == to {
			return lane, true
		}
	}
	return domain.TransferLane{}, false
}

type ChangeFulfillmentWarehouseRequest struct {
	OrderID, ToWarehouse, Reason, IdempotencyKey string
	DryRun                                       bool
}

func (s *Service) ChangeFulfillmentWarehouse(req ChangeFulfillmentWarehouseRequest) (domain.FulfillmentWarehouseChange, error) {
	if req.IdempotencyKey == "" {
		return domain.FulfillmentWarehouseChange{}, ErrIdempotencyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.idempotency["fulfillment-warehouse:"+req.IdempotencyKey]; ok {
		return cached.(domain.FulfillmentWarehouseChange), nil
	}
	order, ok := s.orders[req.OrderID]
	if !ok {
		return domain.FulfillmentWarehouseChange{}, ErrNotFound
	}
	if (order.Status != domain.OrderAwaitingShipment && order.Status != domain.OrderPartiallyShipped) || req.ToWarehouse == "" || req.ToWarehouse == order.FulfillmentWH {
		return domain.FulfillmentWarehouseChange{}, ErrInvalidTransition
	}
	for _, item := range order.Items {
		balance, exists := s.inventory[inventoryKey(req.ToWarehouse, item.SKU)]
		if !exists || balance.Available < item.Quantity {
			return domain.FulfillmentWarehouseChange{}, ErrInsufficientStock
		}
	}
	change := domain.FulfillmentWarehouseChange{
		OrderID: req.OrderID, FromWarehouse: order.FulfillmentWH, ToWarehouse: req.ToWarehouse,
		Reason: req.Reason, Status: "validated",
	}
	if req.DryRun {
		return change, nil
	}
	for _, item := range order.Items {
		key := inventoryKey(req.ToWarehouse, item.SKU)
		balance := s.inventory[key]
		balance.Available -= item.Quantity
		balance.Reserved += item.Quantity
		s.inventory[key] = balance
	}
	order.FulfillmentWH = req.ToWarehouse
	s.orders[order.ID] = order
	change.ID = fmt.Sprintf("FW-%04d", len(s.warehouseChanges)+1)
	change.Status = "changed"
	change.CreatedAt = time.Now().UTC()
	s.warehouseChanges[change.ID] = change
	s.idempotency["fulfillment-warehouse:"+req.IdempotencyKey] = change
	return change, nil
}

type RefundRequest struct {
	OrderID, Reason, IdempotencyKey string
	Amount                          float64
	DryRun                          bool
}

func (s *Service) ApproveRefund(req RefundRequest) (domain.Refund, error) {
	if req.IdempotencyKey == "" {
		return domain.Refund{}, ErrIdempotencyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.idempotency["refund:"+req.IdempotencyKey]; ok {
		return cached.(domain.Refund), nil
	}
	order, ok := s.orders[req.OrderID]
	if !ok {
		return domain.Refund{}, ErrNotFound
	}
	if order.Status != domain.OrderAwaitingShipment || !order.BuyerCancellationRequested || req.Amount <= 0 || req.Amount > order.Amount {
		return domain.Refund{}, ErrInvalidTransition
	}
	if req.DryRun {
		return domain.Refund{OrderID: order.ID, Amount: req.Amount, Currency: order.Currency, Reason: req.Reason, Status: "validated"}, nil
	}
	order.Status = domain.OrderCancelled
	s.orders[order.ID] = order
	refund := domain.Refund{ID: fmt.Sprintf("RF-%04d", len(s.refunds)+1), OrderID: order.ID, Amount: req.Amount, Currency: order.Currency, Reason: req.Reason, Status: "approved", CreatedAt: time.Now().UTC()}
	s.refunds[refund.ID] = refund
	s.idempotency["refund:"+req.IdempotencyKey] = refund
	return refund, nil
}

func (s *Service) GetStatement(id string) (domain.SettlementStatement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	statement, ok := s.statements[id]
	if !ok {
		return domain.SettlementStatement{}, ErrNotFound
	}
	return statement, nil
}

type ReconciliationRequest struct {
	StatementID, Reason, IdempotencyKey string
	DryRun                              bool
}

func (s *Service) CreateReconciliation(req ReconciliationRequest) (domain.ReconciliationCase, error) {
	if req.IdempotencyKey == "" {
		return domain.ReconciliationCase{}, ErrIdempotencyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.idempotency["reconciliation:"+req.IdempotencyKey]; ok {
		return cached.(domain.ReconciliationCase), nil
	}
	statement, ok := s.statements[req.StatementID]
	if !ok {
		return domain.ReconciliationCase{}, ErrNotFound
	}
	if statement.Status != "difference_detected" {
		return domain.ReconciliationCase{}, ErrInvalidTransition
	}
	if req.DryRun {
		return domain.ReconciliationCase{StatementID: statement.ID, Difference: statement.ExpectedAmount - statement.PaidAmount, Reason: req.Reason, Status: "validated"}, nil
	}
	item := domain.ReconciliationCase{ID: fmt.Sprintf("RC-%04d", len(s.reconciliations)+1), StatementID: statement.ID, Difference: statement.ExpectedAmount - statement.PaidAmount, Reason: req.Reason, Status: "submitted", CreatedAt: time.Now().UTC()}
	s.reconciliations[item.ID] = item
	s.idempotency["reconciliation:"+req.IdempotencyKey] = item
	return item, nil
}

func inventoryKey(warehouse, sku string) string { return warehouse + "|" + sku }
