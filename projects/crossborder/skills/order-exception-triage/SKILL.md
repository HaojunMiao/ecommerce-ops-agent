---
name: order_exception_triage
description: 识别跨境订单的库存、履约时效、物流和取消风险，并生成可执行恢复方案
allowed-tools:
  - search_knowledge_base
  - get_order
  - get_inventory
  - get_shipping_options
  - create_inventory_transfer
  - change_fulfillment_warehouse
  - approve_refund
allowed-kbs:
  - __KB_ID__
requires_network: true
---

## 执行流程

1. 调用 `search_knowledge_base` 检索履约、调拨与退款规则。
2. 调用 `get_order` 获取订单状态、履约仓、商品和最晚发货时间。
3. 仅对 `awaiting_shipment` 或 `partially_shipped` 订单生成履约恢复方案。
4. 为每个 SKU 调用 `get_inventory`，核实库存可用量，以及带 `observed_at`、`estimated_hours`、`estimated_arrival` 的仓间调拨线路。
5. 直接比较线路 `estimated_arrival` 与订单 `ship_by`；仅当前者更早时考虑调拨，不得脱离 `observed_at` 自行假设当前时间或算出未提供的到仓时间。
6. 调用 `get_shipping_options` 检查当前履约仓；若调拨来不及，可对有库存的候选仓传入 `warehouse_id`，验证候选仓是否存在 `sla_eligible=true` 的发货渠道。
7. 当前仓缺货、调拨无法在 `ship_by` 前到达，而候选仓库存充足且有 SLA 可行渠道时，可调用 `change_fulfillment_warehouse`，但必须等待人工审批。
8. 按原仓直接履约、可按时到仓的调拨、切换履约仓、取消退款的优先级生成方案，列出成本与风险。
9. 写操作必须携带稳定的 `idempotency_key`，并等待人工审批。
10. 审批恢复后，如果敏感工具返回了调拨单、履约仓变更单或退款单 ID，表示审批已经通过且操作已经执行；最终回答必须报告实际 ID 和状态，不得再表述为“等待审批”。

## 禁止事项

- 不得编造库存、物流价格和平台状态。
- 不得绕过订单状态机。
- 缺少订单、库存或 SLA 证据时转人工运营确认。
