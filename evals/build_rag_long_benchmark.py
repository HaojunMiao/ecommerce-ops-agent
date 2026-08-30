#!/usr/bin/env python3
"""Build a long-document RAG benchmark with independently authored queries.

Documents are generated from policy facts, while query wording lives in a
separate manually authored bank. Policy-level dev/test splitting prevents
chunk and retrieval parameters from being selected on the reported test topics.
All facts are fictional.
"""
from __future__ import annotations

import importlib.util
import json
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "evals" / "corpus" / "rag-long-benchmark"
CASES = ROOT / "evals" / "rag_long_benchmark_cases.jsonl"
MANIFEST = ROOT / "evals" / "rag_long_benchmark_manifest.json"


def load_short_builder():
    path = ROOT / "evals" / "build_rag_benchmark.py"
    spec = importlib.util.spec_from_file_location("rag_short_builder", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


BASE = load_short_builder()

# This query bank is deliberately separate from the document templates. Each
# tuple is (fact_index, category, natural-language query). Wording includes
# colloquial paraphrases, implicit intent, abbreviations and English tokens.
QUERY_BANK = {
    "us_fulfillment": [
        (0, "paraphrase", "美国现货单付款后，仓库最迟什么时候得把货交给物流商？"),
        (1, "implicit", "旺季爆单时，美国仓最多还能多争取几个小时发出？"),
        (2, "exact_code", "给 US 延迟履约做登记时，系统里的 reason code 填什么？"),
        (0, "colloquial", "美区普通单已经付钱了，压仓多久就算超时？"),
    ],
    "eu_returns": [
        (0, "paraphrase", "欧洲普通商品已经收到了，买家反悔的期限有多长？"),
        (1, "implicit", "欧盟订单到货发现摔坏，照片证据过多久就不再受理？"),
        (2, "exact_code", "现在欧区售后采用的退货规则编号是哪一个？"),
        (0, "colloquial", "EU 客户签收一个月后还能走普通退货吗？"),
    ],
    "refund_control": [
        (0, "implicit", "客户申请退款时，系统最多能退到他真正付出去的钱还是标价？"),
        (1, "scenario", "三件商品已经寄出一件，另外两件取消时退款范围怎么算？"),
        (2, "exact_code", "退款防重所用 key 的拼接格式是什么？"),
        (0, "colloquial", "做售后能不能按吊牌价给钱，即使买家当时用了优惠券？"),
    ],
    "settlement_claim": [
        (0, "paraphrase", "核对平台打款时，应收减实收还是反过来才是差额？"),
        (1, "scenario", "账单多付或少付都能自动发起申诉吗，系统具体卡什么条件？"),
        (2, "exact_code", "对账单 STMT-2026-31 需要追讨多少美元？"),
        (0, "implicit", "结算少到账时用于判断申诉方向的计算式是什么？"),
    ],
    "inventory_reservation": [
        (0, "paraphrase", "调货占住的库存如果一直没执行，隔多久会自动放回？"),
        (1, "implicit", "页面上的 Available 还能不能再减一次冻结量？"),
        (2, "scenario", "履约仓没货时，可不可以把另一个仓的数字直接算成本仓可卖？"),
        (0, "colloquial", "跨仓预留过了一整天还有效吗？"),
    ],
    "remote_shipping": [
        (0, "scenario", "发往 Alaska 的普通包裹，前台应该展示多长的预计送达区间？"),
        (1, "scenario", "一个1kg包裹寄到偏远邮编，要额外加收多少美元？"),
        (2, "implicit", "PO Box 不能按普通地址走，系统优先推荐哪条线？"),
        (0, "colloquial", "夏威夷订单通常要等一两周还是七天就到？"),
    ],
    "chargeback_evidence": [
        (0, "paraphrase", "银行拒付通知进来后，商家留给举证的自然日有多少？"),
        (1, "scenario", "处理 card chargeback 时，只有金额截图够不够，还缺哪两类材料？"),
        (2, "exact_code", "拒付案件防重复提交的 key 怎么写？"),
        (0, "colloquial", "拒付邮件拖了八天才处理是不是已经过期？"),
    ],
    "eu_ior_customs": [
        (0, "scenario", "一票149欧元的欧盟进口件能否使用简化税务编号？"),
        (1, "implicit", "IOSS 订单做电子申报时必须随报文带上什么？"),
        (2, "scenario", "商品价值超过低值门槛以后应该切换到哪种进口申报？"),
        (0, "colloquial", "两百欧的包裹还能继续按 IOSS 清关吗？"),
    ],
    "preorder_policy": [
        (0, "paraphrase", "标成 preorder 的商品最多允许商家准备多少个自然日？"),
        (1, "scenario", "预售承诺日期已经过了但还没发货，买家取消要承担责任吗？"),
        (2, "exact_code", "预售逾期无责取消对应哪个状态码？"),
        (0, "colloquial", "预售压二十天不发货符合现在规则吗？"),
    ],
    "battery_shipping": [
        (0, "paraphrase", "电池装在设备里面一起寄，纸箱外面应该贴哪个 UN 标识？"),
        (1, "scenario", "四台内置锂电设备能不能合在一个外箱发走？"),
        (2, "implicit", "承运商没有危险品航空资质时，这类货还能交给它吗？"),
        (0, "exact_code", "设备随附锂离子电池对应 UN3480 还是 UN3481？"),
    ],
    "warehouse_transfer": [
        (0, "scenario", "履约仓差8件，跨仓单应该建8件还是可以多备一些？"),
        (1, "implicit", "在途货很快到仓时不用马上调拨，这里的“很快”具体是多久？"),
        (2, "scenario", "Agent 判断缺货后能直接执行真实调货吗，还是必须等人确认？"),
        (0, "colloquial", "调拨数量按安全库存算还是只补当前缺口？"),
    ],
    "seller_health": [
        (0, "paraphrase", "美区店铺晚发订单占比达到多少会越过健康线？"),
        (1, "implicit", "因为卖家原因取消的订单比例最多允许到多少？"),
        (2, "scenario", "店铺绩效是只看本周，还是连续滚动观察若干周？"),
        (0, "colloquial", "US 店铺迟发率5%还能算健康吗？"),
    ],
    "fx_settlement": [
        (0, "scenario", "欧元订单换成美元结算，系统取哪个机构、什么时间的参考价？"),
        (1, "implicit", "汇率应该在下单那天固定，还是到真正结算时才固定？"),
        (2, "paraphrase", "货币换算尚未最终入账时，中间结果需要留几位小数？"),
        (0, "colloquial", "EUR 转 USD 用支付渠道第二天早上的价格对吗？"),
    ],
    "shipping_label": [
        (0, "paraphrase", "平台买好的物流标签生成后，过多少小时就不能再交运？"),
        (1, "scenario", "同一票货重新打了一张面单，旧的那张还能扫码使用吗？"),
        (2, "implicit", "一个 tracking number 被仓库扫过一次后还能再次揽收吗？"),
        (0, "colloquial", "周一生成的预付面单到周五还能用吗？"),
    ],
    "damaged_return": [
        (0, "scenario", "客户说箱内商品摔坏，只上传一个角度的照片是否满足举证？"),
        (1, "paraphrase", "运输破损从签收开始算，最晚多久必须创建售后？"),
        (2, "scenario", "实付18美元的破损商品是否可以申请不寄回直接退款？"),
        (0, "colloquial", "破损售后到底要拍几张不同方向的图？"),
    ],
    "platform_commission": [
        (0, "paraphrase", "美国普通类目每成交100美元，标准平台抽成比例是多少？"),
        (1, "implicit", "计算平台佣金的金额里面要不要把 sales tax 算进去？"),
        (2, "scenario", "活动报名审核通过以后，订单按哪个优惠佣金率收费？"),
        (0, "colloquial", "美区普通商品现在还是收8个点的平台费吗？"),
    ],
}

TEST_POLICIES = {
    "warehouse_transfer", "seller_health", "fx_settlement",
    "shipping_label", "damaged_return", "platform_commission",
}

GENERAL_PARAGRAPHS = [
    "处理人员首先确认站点、订单类型、币种和履约节点，再读取当前生效版本。截图、聊天记录和历史工单只能作为线索，不能覆盖正式规则。所有判断都要记录输入、适用范围和最终依据，方便复核。",
    "自动化流程分为读取、校验、建议和执行四个阶段。读取阶段不得写入业务系统；校验阶段需要检查字段完整性与时间口径；建议阶段必须说明原因；任何会改变资金、库存或外部状态的动作都需要按系统权限继续处理。",
    "遇到市场、商品类型或时间范围不一致时，不得把相似案例直接套用。应先缩小问题范围，再定位对应版本。数值、比例、时限、公式和代码必须来自同一版本，禁止从多份历史材料中拼接答案。",
]


def boundary_paragraph(label: str, value: str, scope: str) -> str:
    lead = (
        f"本段专门记录{scope}的边界处理。操作员需要先核对市场、时间戳、商品属性、当前状态和已有证据，"
        "再依次排除测试订单、重复请求、历史截图、已撤销记录以及不属于本规则的异常场景。"
    )
    filler = (
        "审核过程中应保持原始字段不变，并把每一步判断写入记录；如果来源不完整，应停止自动处理并请求补充材料。"
        "系统显示的提示语不能替代正式政策，培训示例也不得作为数值依据。"
    )
    prefix = lead
    while len(prefix) < 485:
        prefix += filler
    prefix = prefix[:485]
    return prefix + f"{label}的当前明确要求为：{value}。" + filler * 2


def long_policy_document(policy: dict, current: bool) -> str:
    version = "2026.08 当前生效版" if current else "2025.03 已废止版"
    state = "当前有效" if current else "已废止，不得用于当前决策"
    facts = policy["facts"]
    values = [new if current else old for _, new, old, _ in facts]
    parts = [
        f"# {policy['title']}（{version}）",
        f"> 合成评测材料。版本状态：{state}。适用对象：{policy['scope']}。",
        "## 阅读与适用原则\n\n" + GENERAL_PARAGRAPHS[0] + "\n\n" + GENERAL_PARAGRAPHS[1],
        "## 业务背景\n\n" + GENERAL_PARAGRAPHS[2] + "\n\n" + GENERAL_PARAGRAPHS[0],
        f"## 规则一：{facts[0][0]}\n\n在确认{policy['scope']}且没有例外标记后，{facts[0][0]}的当前明确要求为：{values[0]}。该结论必须和版本号一起写入处理记录。\n\n" + GENERAL_PARAGRAPHS[1],
        f"## 规则二：{facts[1][0]}（边界段）\n\n" + boundary_paragraph(facts[1][0], values[1], policy["scope"]),
        f"## 规则三：{facts[2][0]}\n\n完成前置核验后，{facts[2][0]}的当前明确要求为：{values[2]}。代码、公式或渠道名称必须完整保留大小写、符号和单位。\n\n" + GENERAL_PARAGRAPHS[2],
        "## 例外、升级与人工复核\n\n" + GENERAL_PARAGRAPHS[0] + "\n\n" + GENERAL_PARAGRAPHS[2] + "\n\n" + GENERAL_PARAGRAPHS[1],
        "## 审计清单\n\n处理完成后复核规则版本、输入证据、计算过程、候选方案和审批状态。涉及资金、库存、物流提交或申诉创建时，必须保留可追踪的业务标识，不得因为模型给出确定语气而跳过控制步骤。",
        "## 附录：复核案例与记录规范\n\n" + "\n\n".join(GENERAL_PARAGRAPHS * 4),
    ]
    return "\n\n".join(parts) + "\n"


def long_distractor(i: int, policy: dict) -> str:
    labels = "、".join(f[0] for f in policy["facts"])
    paragraphs = "\n\n".join(GENERAL_PARAGRAPHS * 7)
    return f"""# {policy['title']}培训手册 {i + 1}

> 合成干扰文档。只解释流程，不包含可执行数值。

本文讨论{policy['scope']}中的{labels}，但所有阈值、时间、金额、公式、代码和渠道名称均被刻意省略。培训人员应引导操作者查找当前正式规则，而不是根据本文猜测答案。

{paragraphs}

## 练习说明

练习只要求识别信息缺口、版本冲突和需要人工复核的节点。本文不能作为业务执行依据。
"""


def long_background(i: int) -> str:
    base = BASE.background_document(i).strip()
    extra = "\n\n".join(GENERAL_PARAGRAPHS * 10)
    return base + "\n\n## 扩展背景\n\n" + extra + "\n"


def write_subset(name: str, docs: list[dict]) -> None:
    target = OUT / name
    target.mkdir(parents=True, exist_ok=True)
    for doc in docs:
        (target / f"{doc['id']}.md").write_text(doc["text"], encoding="utf-8")


def main() -> None:
    if set(QUERY_BANK) != {p["id"] for p in BASE.POLICIES}:
        raise RuntimeError("query bank and policy IDs differ")
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir(parents=True)

    business = []
    cases = []
    for policy in BASE.POLICIES:
        current_id = f"{policy['id']}_current"
        retired_id = f"{policy['id']}_retired"
        current_text = long_policy_document(policy, True)
        retired_text = long_policy_document(policy, False)
        business.extend([
            {"id": current_id, "tier": "current", "text": current_text},
            {"id": retired_id, "tier": "hard_negative", "text": retired_text},
        ])
        split = "test" if policy["id"] in TEST_POLICIES else "dev"
        for number, (fact_index, category, query) in enumerate(QUERY_BANK[policy["id"]], start=1):
            gold_span = policy["facts"][fact_index][1]
            if gold_span not in current_text:
                raise RuntimeError(f"gold span missing: {policy['id']} #{number}")
            original_question = policy["facts"][fact_index][3]
            if query == original_question:
                raise RuntimeError(f"query copied from source specification: {policy['id']} #{number}")
            cases.append({
                "id": f"long_{policy['id']}_{number}",
                "policy_id": policy["id"],
                "split": split,
                "category": "boundary" if fact_index == 1 else category,
                "query": query,
                "gold_docs": [current_id],
                "gold_span": gold_span,
            })

    distractors = [
        {"id": f"domain_distractor_{i:02d}", "tier": "domain_distractor", "text": long_distractor(i, p)}
        for i, p in enumerate(BASE.POLICIES)
    ]
    background = [
        {"id": f"background_{i:03d}", "tier": "background", "text": long_background(i)}
        for i in range(120)
    ]
    core = business + distractors
    large = core + background
    active_large = [doc for doc in business if doc["tier"] == "current"] + distractors + background
    write_subset("core", core)
    write_subset("active120", active_large)
    write_subset("noise120", large)
    CASES.write_text("".join(json.dumps(c, ensure_ascii=False) + "\n" for c in cases), encoding="utf-8")

    lengths = [len(doc["text"]) for doc in large]
    manifest = {
        "synthetic": True,
        "description": "长文档独立查询RAG评测；事实均为虚构。",
        "query_authorship": "independent manually-authored query bank; not document template questions",
        "split": "policy-level holdout: 10 dev policies / 6 test policies",
        "subsets": {"core": len(core), "active120": len(active_large), "noise120": len(large)},
        "tiers": {"current": 16, "hard_negative": 16, "domain_distractor": 16, "background": 120},
        "queries": len(cases),
        "dev_queries": sum(c["split"] == "dev" for c in cases),
        "test_queries": sum(c["split"] == "test" for c in cases),
        "document_chars": {"min": min(lengths), "max": max(lengths), "mean": round(sum(lengths) / len(lengths), 1)},
    }
    MANIFEST.write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(manifest, ensure_ascii=False))


if __name__ == "__main__":
    main()
