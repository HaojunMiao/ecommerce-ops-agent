package domain

import "time"

// 定义跨境电商业务中的核心数据

type OrderStatus string

const (
	OrderAwaitingShipment OrderStatus = "awaiting_shipment"
	OrderCancelled        OrderStatus = "cancelled"
)

// 订单中的单个商品
type OrderItem struct {
	SKU      string
	Quantity int
	Price    float64
}

// 订单
type Order struct {
	ID               string
	Market           string
	Currency         string
	Amount           float64
	Status           OrderStatus
	FulfillmentWH    string
	CancellationOpen bool
	Items            []OrderItem
}

// 某个仓库中某个SKU的可用库存
type InventoryBalance struct {
	WarehouseID string
	SKU         string
	Available   int
}

// 仓库之间的调拨记录
// 从哪调到哪、调哪个（SKU）、调多少件（Quantity）
type InventoryTransfer struct {
	ID            string
	SKU           string
	FromWarehouse string
	ToWarehouse   string
	Quantity      int
	Status        string
	CreatedAt     time.Time
}
