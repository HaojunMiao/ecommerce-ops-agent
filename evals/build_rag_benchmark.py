#!/usr/bin/env python3
"""Build a deterministic, synthetic cross-border RAG benchmark corpus.

The benchmark deliberately contains current policies, near-duplicate retired
policies, same-domain distractors, and unrelated background documents.  Facts
are fictional and carry stable markers so retrieval quality can be measured
without an LLM judge.
"""
from __future__ import annotations

import json
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "evals" / "corpus" / "rag-benchmark"
CASES = ROOT / "evals" / "rag_benchmark_cases.jsonl"
MANIFEST = ROOT / "evals" / "rag_benchmark_manifest.json"


POLICIES = [
    {
        "id": "us_fulfillment",
        "title": "US站履约时效",
        "scope": "US站标准现货订单",
        "facts": [
            ("交接时限", "支付后8小时内完成承运商交接", "支付后12小时内完成承运商交接", "US站现货订单支付后多久必须交给承运商"),
            ("高峰延期", "最多延长至12小时", "最多延长至18小时", "US站高峰期履约窗口最多能延长到多久"),
            ("延期原因码", "SLA-DELAY-12H", "SLA-DELAY-18H", "US站申请高峰延期要填写哪个原因码"),
        ],
    },
    {
        "id": "eu_returns",
        "title": "EU站退货窗口",
        "scope": "EU站普通商品退货",
        "facts": [
            ("退货窗口", "签收后30个自然日", "签收后14个自然日", "EU站普通商品签收后多少天内可以退货"),
            ("破损举证", "签收后48小时内提交", "签收后72小时内提交", "EU站破损商品最晚多久提交证据"),
            ("规则代码", "EU-RET-30", "EU-RET-14", "EU站当前退货规则代码是什么"),
        ],
    },
    {
        "id": "refund_control",
        "title": "退款金额与幂等",
        "scope": "未完全发货订单退款",
        "facts": [
            ("退款基数", "以订单实付金额为上限", "以商品吊牌价为上限", "退款金额上限应该按实付金额还是吊牌价"),
            ("部分发货", "只退未发货行的分摊金额", "允许直接整单退款", "订单部分发货后应该怎么退款"),
            ("幂等键", "refund:{order_id}:{amount}", "refund:{user_id}:{date}", "退款接口的稳定幂等键格式是什么"),
        ],
    },
    {
        "id": "settlement_claim",
        "title": "结算差异申诉",
        "scope": "跨境平台账单对账",
        "facts": [
            ("差异公式", "expected_amount - paid_amount", "paid_amount - expected_amount", "当前结算差异应该怎么计算"),
            ("自动申诉条件", "仅正差异允许自动创建申诉", "所有非零差异都自动申诉", "什么样的结算差异可以自动申诉"),
            ("演示差异", "STMT-2026-31的差异为11.52 USD", "STMT-2026-31的差异为-11.52 USD", "STMT-2026-31当前应申诉的差异金额是多少"),
        ],
    },
    {
        "id": "inventory_reservation",
        "title": "库存预占",
        "scope": "多仓库存与调拨预占",
        "facts": [
            ("预占期限", "调拨预占保留24小时", "调拨预占保留48小时", "调拨库存预占多久会释放"),
            ("可用量口径", "Available已经扣除预占和冻结", "Available需要再扣一次预占", "库存Available是否已经扣除了预占和冻结"),
            ("跨仓汇总", "不同仓库可用量不得合并冒充履约仓库存", "允许把所有仓可用量直接相加", "能否把其他仓库存加到当前履约仓可用量里"),
        ],
    },
    {
        "id": "remote_shipping",
        "title": "偏远地区配送",
        "scope": "阿拉斯加、夏威夷和PO Box地址",
        "facts": [
            ("默认时效", "默认配送时效为12至18日", "默认配送时效为7日", "阿拉斯加地址当前默认配送时效是多少"),
            ("中量附加费", "0.5至2kg收取11.0 USD", "0.5至2kg收取8.0 USD", "偏远地址1公斤包裹收多少附加费"),
            ("推荐渠道", "4PX Remote Saver", "4PX US Priority", "PO Box地址应优先选择哪个物流渠道"),
        ],
    },
    {
        "id": "chargeback_evidence",
        "title": "拒付举证",
        "scope": "银行卡拒付争议",
        "facts": [
            ("提交期限", "收到拒付通知后7个自然日内", "收到拒付通知后14个自然日内", "收到拒付通知后几天内必须提交证据"),
            ("必要证据", "必须包含物流签收证明和订单沟通记录", "只需要上传订单金额截图", "拒付申诉至少要提供哪些证据"),
            ("案件幂等键", "chargeback:{case_id}", "chargeback:{order_id}:{date}", "拒付案件使用什么幂等键"),
        ],
    },
    {
        "id": "eu_ior_customs",
        "title": "EU低价值包裹IOSS",
        "scope": "EU进口低价值商品",
        "facts": [
            ("适用阈值", "货值不超过150 EUR时使用IOSS", "货值不超过200 EUR时使用IOSS", "EU包裹货值多少以内适用IOSS"),
            ("报关要求", "电子报关必须携带平台IOSS号码", "IOSS号码只需写在纸质面单", "使用IOSS时电子报关要带什么信息"),
            ("超限处理", "超过150 EUR改走普通进口申报", "超过150 EUR仍继续使用IOSS", "EU包裹超过IOSS阈值后怎么申报"),
        ],
    },
    {
        "id": "preorder_policy",
        "title": "预售订单",
        "scope": "标记为preorder的订单",
        "facts": [
            ("最长备货", "最长备货周期为14个自然日", "最长备货周期为21个自然日", "预售订单当前最长可以备货多少天"),
            ("超期处理", "超过承诺日仍未发货必须允许无责取消", "超期后仍禁止买家取消", "预售订单超过承诺发货日后买家能否取消"),
            ("状态代码", "PREORDER-LATE-CANCEL", "PREORDER-HOLD-21", "预售超期取消使用哪个状态代码"),
        ],
    },
    {
        "id": "battery_shipping",
        "title": "含锂电商品运输",
        "scope": "设备内置锂离子电池",
        "facts": [
            ("包装标识", "外箱必须标记UN3481", "外箱只需标记UN3480", "设备内置锂电池外箱应标哪个UN编号"),
            ("单箱数量", "单箱最多放置2台设备", "单箱最多放置6台设备", "含锂电设备一箱最多放几台"),
            ("运输限制", "禁止使用无危险品资质的航空渠道", "所有普通航空渠道均可使用", "含锂电商品能否走无危险品资质的航空渠道"),
        ],
    },
    {
        "id": "warehouse_transfer",
        "title": "跨仓调拨审批",
        "scope": "履约仓缺货后的跨仓调拨",
        "facts": [
            ("调拨数量", "调拨数量必须等于库存缺口", "允许按缺口的两倍调拨", "跨仓调拨数量应该按什么口径确定"),
            ("补货等待", "4小时内有在途补货时优先等待补货", "12小时内有在途补货时都必须等待", "多久内能到的在途补货应优先等待"),
            ("执行约束", "真实调拨必须经过人工审批", "真实调拨可以由模型直接执行", "跨仓调拨是否必须经过人工审批"),
        ],
    },
    {
        "id": "seller_health",
        "title": "卖家履约健康度",
        "scope": "US站卖家周度绩效",
        "facts": [
            ("迟发率", "迟发率必须低于4%", "迟发率必须低于8%", "US站卖家当前迟发率阈值是多少"),
            ("卖家取消率", "卖家责任取消率必须低于2%", "卖家责任取消率必须低于5%", "US站卖家责任取消率上限是多少"),
            ("观察周期", "连续4周滚动观察", "连续2周滚动观察", "卖家履约健康度按多长周期滚动观察"),
        ],
    },
    {
        "id": "fx_settlement",
        "title": "跨币种结算汇率",
        "scope": "EUR订单结算为USD",
        "facts": [
            ("汇率来源", "使用ECB工作日16:00 UTC参考汇率", "使用付款平台次日08:00汇率", "EUR订单结算当前采用哪个时间点的汇率"),
            ("锁定日期", "以订单结算日锁定汇率", "以订单创建日锁定汇率", "跨币种订单在哪一天锁定结算汇率"),
            ("精度", "中间计算保留4位小数", "中间计算保留2位小数", "汇率换算中间结果保留几位小数"),
        ],
    },
    {
        "id": "shipping_label",
        "title": "物流面单有效期",
        "scope": "平台生成的预付费面单",
        "facts": [
            ("有效期", "面单生成后72小时有效", "面单生成后168小时有效", "平台预付费面单生成后有效多久"),
            ("重打规则", "重新打印会立即作废旧面单", "重新打印后旧面单仍可使用", "重新打印物流面单后旧面单还能用吗"),
            ("扫描限制", "同一运单号只允许首次揽收扫描生效", "同一运单号允许重复揽收", "同一个运单号可以重复做揽收扫描吗"),
        ],
    },
    {
        "id": "damaged_return",
        "title": "破损商品售后",
        "scope": "签收时发现运输破损",
        "facts": [
            ("照片数量", "至少提交3张不同角度照片", "提交1张照片即可", "运输破损售后至少需要几张照片"),
            ("提交时限", "签收后48小时内发起", "签收后7天内发起", "运输破损最晚在签收后多久发起"),
            ("免退阈值", "商品实付不超过20 USD可审核免退退款", "商品实付不超过50 USD自动免退", "破损商品多少钱以内可以审核免退退款"),
        ],
    },
    {
        "id": "platform_commission",
        "title": "平台佣金计算",
        "scope": "US站普通类目订单",
        "facts": [
            ("标准费率", "标准佣金率为6.5%", "标准佣金率为8.0%", "US站普通类目当前标准佣金率是多少"),
            ("计费基数", "佣金基数不包含销售税", "佣金基数包含销售税", "平台佣金计算是否包含销售税"),
            ("活动费率", "活动期审核通过后费率为4.8%", "活动期自动降为3.0%", "审核通过的活动订单佣金率是多少"),
        ],
    },
]


BACKGROUND_TOPICS = [
    "关系数据库事务隔离", "HTTP缓存控制", "容器镜像分层", "城市公共交通", "家庭园艺灌溉",
    "摄影曝光基础", "古典音乐曲式", "操作系统调度", "气象观测方法", "食品冷藏原则",
    "软件测试分层", "网络路由协议", "数据结构与算法", "项目管理风险", "建筑节能设计",
    "机器学习特征工程", "天文望远镜维护", "语言学习计划", "博物馆藏品管理", "体育训练恢复",
]


def policy_document(policy: dict, current: bool) -> str:
    version = "2026.08 当前生效版" if current else "2025.03 已废止版"
    state = "当前有效，仅用于合成评测。" if current else "已废止，仅用于检索困难负样本，不得作为当前规则。"
    facts = policy["facts"]
    lines = [
        f"# {policy['title']}（{version}）",
        "",
        f"> 数据性质：合成评测文档。状态：{state}",
        "",
        "## 适用范围",
        "",
        f"本规则适用于{policy['scope']}。不同市场、不同版本不得混用，执行前必须确认规则状态。",
    ]
    for idx, (label, new, old, _) in enumerate(facts, start=1):
        value = new if current else old
        lines.extend([
            "",
            f"## {idx}. {label}",
            "",
            f"{label}的明确要求为：{value}。操作记录必须保存规则版本、业务标识和判断证据。",
            f"如果输入条件不属于{policy['scope']}，不得直接套用本条规则，应转交对应市场政策处理。",
        ])
    lines.extend([
        "",
        "## 审计说明",
        "",
        "所有自动建议必须保留检索到的规则片段；涉及资金、库存或申诉的写操作仍需人工确认。",
    ])
    return "\n".join(lines) + "\n"


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


def write_subset(name: str, docs: list[dict]) -> None:
    target = OUT / name
    target.mkdir(parents=True, exist_ok=True)
    for doc in docs:
        (target / f"{doc['id']}.md").write_text(doc["text"], encoding="utf-8")


def main() -> None:
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir(parents=True)

    business: list[dict] = []
    cases: list[dict] = []
    for policy in POLICIES:
        current_id = f"{policy['id']}_current"
        retired_id = f"{policy['id']}_retired"
        business.extend([
            {"id": current_id, "tier": "current", "text": policy_document(policy, True)},
            {"id": retired_id, "tier": "hard_negative", "text": policy_document(policy, False)},
        ])
        for index, (_, current, _, question) in enumerate(policy["facts"], start=1):
            cases.append({
                "id": f"{policy['id']}_{index}",
                "category": "exact" if index == 3 else "semantic",
                "query": question,
                "gold_docs": [current_id],
                "gold_span": current,
            })

    distractors = [
        {"id": f"domain_distractor_{i:02d}", "tier": "domain_distractor", "text": domain_distractor(i, p)}
        for i, p in enumerate(POLICIES)
    ]
    background = [
        {"id": f"background_{i:03d}", "tier": "background", "text": background_document(i)}
        for i in range(120)
    ]
    core = business + distractors
    medium = core + background[:40]
    large = core + background
    write_subset("core", core)
    write_subset("noise40", medium)
    write_subset("noise120", large)

    CASES.write_text("".join(json.dumps(c, ensure_ascii=False) + "\n" for c in cases), encoding="utf-8")
    manifest = {
        "synthetic": True,
        "description": "受控跨境电商RAG评测语料；事实均为虚构，禁止作为真实业务规则。",
        "subsets": {"core": len(core), "noise40": len(medium), "noise120": len(large)},
        "tiers": {
            "current": len(POLICIES),
            "hard_negative": len(POLICIES),
            "domain_distractor": len(distractors),
            "background": len(background),
        },
        "queries": len(cases),
    }
    MANIFEST.write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(manifest, ensure_ascii=False))


if __name__ == "__main__":
    main()
