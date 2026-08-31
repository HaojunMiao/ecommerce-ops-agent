#!/usr/bin/env python3
"""Shared synthetic policy templates for the RAG evaluation dataset.

Facts are fictional and carry stable markers so retrieval quality can be
measured without an LLM judge.
"""
from __future__ import annotations

POLICIES = [
    {
        "id": "us_fulfillment",
        "title": "US站履约时效",
        "scope": "US站标准现货订单",
        "facts": [
            ('交接时限', '支付后8小时内完成承运商交接'),
            ('高峰延期', '最多延长至12小时'),
            ('延期原因码', 'SLA-DELAY-12H'),
        ],
    },
    {
        "id": "eu_returns",
        "title": "EU站退货窗口",
        "scope": "EU站普通商品退货",
        "facts": [
            ('退货窗口', '签收后30个自然日'),
            ('破损举证', '签收后48小时内提交'),
            ('规则代码', 'EU-RET-30'),
        ],
    },
    {
        "id": "refund_control",
        "title": "退款金额与幂等",
        "scope": "未完全发货订单退款",
        "facts": [
            ('退款基数', '以订单实付金额为上限'),
            ('部分发货', '只退未发货行的分摊金额'),
            ('幂等键', 'refund:{order_id}:{amount}'),
        ],
    },
    {
        "id": "settlement_claim",
        "title": "结算差异申诉",
        "scope": "跨境平台账单对账",
        "facts": [
            ('差异公式', 'expected_amount - paid_amount'),
            ('自动申诉条件', '仅正差异允许自动创建申诉'),
            ('演示差异', 'STMT-2026-31的差异为11.52 USD'),
        ],
    },
    {
        "id": "inventory_reservation",
        "title": "库存预占",
        "scope": "多仓库存与调拨预占",
        "facts": [
            ('预占期限', '调拨预占保留24小时'),
            ('可用量口径', 'Available已经扣除预占和冻结'),
            ('跨仓汇总', '不同仓库可用量不得合并冒充履约仓库存'),
        ],
    },
    {
        "id": "remote_shipping",
        "title": "偏远地区配送",
        "scope": "阿拉斯加、夏威夷和PO Box地址",
        "facts": [
            ('默认时效', '默认配送时效为12至18日'),
            ('中量附加费', '0.5至2kg收取11.0 USD'),
            ('推荐渠道', '4PX Remote Saver'),
        ],
    },
    {
        "id": "chargeback_evidence",
        "title": "拒付举证",
        "scope": "银行卡拒付争议",
        "facts": [
            ('提交期限', '收到拒付通知后7个自然日内'),
            ('必要证据', '必须包含物流签收证明和订单沟通记录'),
            ('案件幂等键', 'chargeback:{case_id}'),
        ],
    },
    {
        "id": "eu_ior_customs",
        "title": "EU低价值包裹IOSS",
        "scope": "EU进口低价值商品",
        "facts": [
            ('适用阈值', '货值不超过150 EUR时使用IOSS'),
            ('报关要求', '电子报关必须携带平台IOSS号码'),
            ('超限处理', '超过150 EUR改走普通进口申报'),
        ],
    },
    {
        "id": "preorder_policy",
        "title": "预售订单",
        "scope": "标记为preorder的订单",
        "facts": [
            ('最长备货', '最长备货周期为14个自然日'),
            ('超期处理', '超过承诺日仍未发货必须允许无责取消'),
            ('状态代码', 'PREORDER-LATE-CANCEL'),
        ],
    },
    {
        "id": "battery_shipping",
        "title": "含锂电商品运输",
        "scope": "设备内置锂离子电池",
        "facts": [
            ('包装标识', '外箱必须标记UN3481'),
            ('单箱数量', '单箱最多放置2台设备'),
            ('运输限制', '禁止使用无危险品资质的航空渠道'),
        ],
    },
]


BACKGROUND_TOPICS = [
    "关系数据库事务隔离", "HTTP缓存控制", "容器镜像分层", "城市公共交通", "家庭园艺灌溉",
    "摄影曝光基础", "古典音乐曲式", "操作系统调度", "气象观测方法", "食品冷藏原则",
    "软件测试分层", "网络路由协议", "数据结构与算法", "项目管理风险", "建筑节能设计",
    "机器学习特征工程", "天文望远镜维护", "语言学习计划", "博物馆藏品管理", "体育训练恢复",
]


def domain_distractor(i: int, policy: dict) -> str:
    labels = "、".join(f[0] for f in policy["facts"])
    return f"""# {policy['title']}培训与术语说明 {i + 1}

> 数据性质：合成评测干扰文档，不包含可执行阈值。

本文用于解释{policy['scope']}中的常见术语，包括{labels}。培训材料只介绍处理流程：确认市场、读取当前版本、收集证据、提交审核、记录结果。本文故意不提供时限、金额、比例、代码或公式，不能替代正式政策。

客服遇到用户询问时，应先定位当前生效规则，再基于正式规则作答。历史示例、截图和个人经验均不得作为最终依据。
"""


def background_document(i: int) -> str:
    topic = BACKGROUND_TOPICS[i % len(BACKGROUND_TOPICS)]
    group = i // len(BACKGROUND_TOPICS) + 1
    return f"""# {topic}背景资料 {group}

> 数据性质：合成背景噪声，和跨境电商业务无关。

本文介绍{topic}的基础概念、常见术语和实践步骤。第{group}组材料强调先确定目标，再收集输入，随后执行过程检查并记录结果。为了形成稳定的规模语料，文档包含编号 BG-{i:04d}，但不包含退款、履约、库存、结算或物流政策答案。

## 方法

实践中应区分事实、假设和结论，保留可复现的输入，并用独立样本验证结果。出现异常时先缩小范围，再检查配置、数据和边界条件，避免凭单一现象得出结论。

## 记录

记录应包含时间、环境、负责人和结果摘要。该段落用于增加语料长度和检索噪声，不构成任何业务规则。
"""
