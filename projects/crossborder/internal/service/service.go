package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/HaojunMiao/go-agent-platform/projects/crossborder/internal/domain"
)

// 模拟跨境电商后台数据库+业务服务
// 数据库表使用Go map模拟
/*
orders 模拟订单表，key为订单ID，value为订单
inventory 模拟库存表，key为仓库+SKU，value为库存记录
transfers 模拟调拨单表，key为调拨单的id，value为调拨记录
idempotency 模拟幂等记录表，key为幂等key，value为请求指纹+上次返回的结果
*/

//

var (
	ErrNotFound            = errors.New("business resource not found")
	ErrInvalidTransition   = errors.New("invalid business state transition")
	ErrInsufficientStock   = errors.New("insufficient inventory")
	ErrIdempotencyKey      = errors.New("idempotency_key is required")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for a different request")
)

// 调货接口请求体
type TransferRequest struct {
	SKU            string `json:"sku"`
	FromWarehouse  string `json:"from_warehouse"`  // 从哪个仓库调货
	ToWarehouse    string `json:"to_warehouse"`    // 调货到哪个仓库
	IdempotencyKey string `json:"idempotency_key"` // 幂等key，避免重复调货
	Quantity       int    `json:"quantity"`        // 调货数量
	DryRun         bool   `json:"dry_run"`         // 是否只是模拟调货，不实际扣减库存
}

// 相关数据存储在内存中，方便测试和演示（不涉及数据库操作，用内存中的map模拟数据库）
type Service struct {
	mu          sync.RWMutex
	orders      map[string]domain.Order
	inventory   map[string]domain.InventoryBalance
	transfers   map[string]domain.InventoryTransfer
	idempotency map[string]idempotencyRecord // 幂等记录表
}

// 记录某个请求是否处理过，以及当时返回了什么
type idempotencyRecord struct {
	fingerprint [32]byte
	transfer    domain.InventoryTransfer
}

// NewSeeded 创建一个带有初始数据的Service实例（创建带有预置订单和库存数据的服务）
// mock 初始订单数据和库存数据（不为了写一个agent项目而从头写一个跨境电商项目）
func NewSeeded() *Service {
	return &Service{
		orders: map[string]domain.Order{
			"TTS-20260801-1001": {
				ID: "TTS-20260801-1001", Market: "US", Currency: "USD",
				Amount: 129.99, Status: domain.OrderAwaitingShipment,
				FulfillmentWH: "WH-CN-SZ", CancellationOpen: true,
				Items: []domain.OrderItem{{SKU: "SKU-BLACK-M-01", Quantity: 1, Price: 129.99}},
			},
		},
		inventory: map[string]domain.InventoryBalance{
			inventoryKey("WH-CN-SZ", "SKU-BLACK-M-01"): {
				WarehouseID: "WH-CN-SZ", SKU: "SKU-BLACK-M-01", Available: 0,
			},
			inventoryKey("WH-US-LAX", "SKU-BLACK-M-01"): {
				WarehouseID: "WH-US-LAX", SKU: "SKU-BLACK-M-01", Available: 18,
			},
		},
		transfers:   make(map[string]domain.InventoryTransfer),
		idempotency: make(map[string]idempotencyRecord),
	}
}

// GetOrder 根据订单ID查询订单
func (s *Service) GetOrder(id string) (domain.Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 模拟 SELECT * FROM orders WHERE id = ?
	order, ok := s.orders[id]
	return order, ok
}

// Inventory 根据SKU查询库存（返回在不同仓库的库存情况）
func (s *Service) Inventory(sku string) []domain.InventoryBalance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.InventoryBalance, 0, len(s.inventory))
	for _, balance := range s.inventory {
		if balance.SKU == sku {
			result = append(result, balance)
		}
	}
	return result
}

// CreateTransfer 创建调货记录（后续会交给大模型调用，如基于历史数据，交给大模型进行调货决策）
// 在这里（业务层面）要保证幂等性，即防止大模型重复调用带来的幂等问题（保证同一业务请求重复到达时只产生一次实际效果）
func (s *Service) CreateTransfer(req TransferRequest) (domain.InventoryTransfer, error) {
	// 1. 参数校验
	// 没传幂等key，直接拒绝执行
	if req.IdempotencyKey == "" {
		return domain.InventoryTransfer{}, ErrIdempotencyKey
	}
	// 调拨数量小于等于0，或来源仓与目标仓相同，报错
	if req.Quantity <= 0 || req.FromWarehouse == req.ToWarehouse {
		return domain.InventoryTransfer{}, ErrInvalidTransition
	}

	// 2. 幂等校验
	// 加互斥锁，保证整套（查库存、改库存）的流程不被并发插入打断，必须作为一个整体执行。
	s.mu.Lock()
	defer s.mu.Unlock()

	// 请求的指纹（用请求中的关键参数构造一个字符串/哈希值，用于唯一标识一个请求）
	fingerprint := transferFingerprint(req)
	if cached, ok := s.idempotency[req.IdempotencyKey]; ok {
		// 查到了这个幂等key
		if cached.fingerprint != fingerprint {
			// 如果不一样，说明同一个幂等key被用在了不同的请求上，返回冲突错误
			return domain.InventoryTransfer{}, ErrIdempotencyConflict
		}
		return cached.transfer, nil // 如果一样，说明是重复请求，直接返回之前的结果
	}

	// 注：idempotencyKey由调用方生成，同一次逻辑操作的所有重试必须使用相同key，新的业务操作使用新的key
	// idempotencyKey的设计是关键，关乎到幂等粒度，标识”这是哪一次业务操作“
	// idempotency表可以理解为数据库表，不是redis缓存。

	// 3. 库存校验
	// 否则，说明是第一次请求，继续处理，拼接调货记录的数据
	fromKey := inventoryKey(req.FromWarehouse, req.SKU) // 生成唯一的库存记录键
	toKey := inventoryKey(req.ToWarehouse, req.SKU)

	// from是来源仓库的库存记录，下面的to是目标仓库的库存记录
	from, ok := s.inventory[fromKey]
	// 库存中找不到这个仓库的这个商品，报错。因为来源库存必须存在（目标库存可以不存在)
	if !ok {
		return domain.InventoryTransfer{}, ErrNotFound
	}
	// 判断库存是否足够调货需求
	if from.Available < req.Quantity {
		return domain.InventoryTransfer{}, ErrInsufficientStock
	}

	// 如果是测试 DryRUn，不用转移库存数据（不修改库存数据、不写入transfers表）
	// 但会写入idempotency表（幂等记录)
	if req.DryRun {
		transfer := domain.InventoryTransfer{
			SKU:           req.SKU,
			FromWarehouse: req.FromWarehouse,
			ToWarehouse:   req.ToWarehouse,
			Quantity:      req.Quantity,
			Status:        "validated",
		}

		s.idempotency[req.IdempotencyKey] = idempotencyRecord{
			fingerprint: fingerprint,
			transfer:    transfer,
		}
		return transfer, nil
	}

	// 4. 创建调库存单子

	// 相当于数据库事务操作，模拟更新库存（from和to就是库存表的两条记录，分别是SKU在来源仓库和目标仓库的库存记录）
	to := s.inventory[toKey]
	to.WarehouseID = req.ToWarehouse
	to.SKU = req.SKU
	from.Available -= req.Quantity // 真正减库存
	to.Available += req.Quantity
	// 更新完from和to两个inventory结构体对象，写回“库存表”中
	s.inventory[fromKey] = from
	s.inventory[toKey] = to

	transfer := domain.InventoryTransfer{
		ID:            fmt.Sprintf("TR-%04d", len(s.transfers)+1),
		SKU:           req.SKU,
		FromWarehouse: req.FromWarehouse,
		ToWarehouse:   req.ToWarehouse,
		Quantity:      req.Quantity,
		Status:        "created",
		CreatedAt:     time.Now().UTC(),
	}
	// 创建的调拨数据行（transfer）写入transfers表（即放进map）
	s.transfers[transfer.ID] = transfer

	// 保存幂等记录
	s.idempotency[req.IdempotencyKey] = idempotencyRecord{
		fingerprint: fingerprint,
		transfer:    transfer,
	}

	return transfer, nil

}

// 通过“仓库 + SKU”生成库存记录的唯一键，即ID
func inventoryKey(warehouse, sku string) string {
	return warehouse + "|" + sku
}

func transferFingerprint(req TransferRequest) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%t", req.SKU, req.FromWarehouse, req.ToWarehouse, req.Quantity, req.DryRun)))
}
