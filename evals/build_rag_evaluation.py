#!/usr/bin/env python3
"""Build the fixed RAG evaluation corpus and independently authored queries.

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
OUT = ROOT / "evals" / "corpus" / "rag-evaluation"
CASES = ROOT / "evals" / "rag_evaluation_cases.jsonl"
MANIFEST = ROOT / "evals" / "rag_evaluation_manifest.json"


def load_templates():
    path = ROOT / "evals" / "_rag_dataset_base.py"
    spec = importlib.util.spec_from_file_location("rag_dataset_base", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


BASE = load_templates()

# Test policies are separate policy-level topics, rather than extra paraphrases
# of development topics.
TEST_POLICY_DEFINITIONS = [
    {
        "id": "vat_invoice", "title": "VAT发票开具", "scope": "EU站企业买家VAT发票",
        "facts": [
            ('申请期限', '订单完成后90个自然日内申请'),
            ('税号校验', 'VAT号码必须通过VIES校验'),
            ('发票代码', 'VAT-INV-EU'),
        ],
    },
    {
        "id": "address_change", "title": "发货地址修改", "scope": "尚未交接承运商的订单",
        "facts": [
            ('允许阶段', '仅在仓库开始拣货前允许自助修改'),
            ('高风险变更', '跨国家或地区修改必须取消后重下单'),
            ('审计事件', 'ADDRESS-CHANGE-V2'),
        ],
    },
    {
        "id": "lost_parcel", "title": "丢件认定与赔付", "scope": "跨境物流长时间无轨迹包裹",
        "facts": [
            ('认定时限', '连续14个自然日无新轨迹可发起丢件调查'),
            ('赔付上限', '赔付不超过订单实付与运费之和'),
            ('案件代码', 'LOST-PARCEL-14D'),
        ],
    },
    {
        "id": "customs_declaration", "title": "出口报关申报", "scope": "跨境出口商业包裹",
        "facts": [
            ('申报价值', '按买家实际支付的商品金额申报'),
            ('品名要求', '必须填写具体英文品名并禁止使用gift'),
            ('币种代码', '申报币种使用ISO 4217三字母代码'),
        ],
    },
    {
        "id": "oversize_shipping", "title": "超大件运输", "scope": "最长边超过常规限制的包裹",
        "facts": [
            ('超大件阈值', '任一边超过120cm即按超大件处理'),
            ('计费重量', '按实重与体积重中的较大值计费'),
            ('服务代码', 'BULKY-PLUS'),
        ],
    },
    {
        "id": "pickup_sla", "title": "上门揽收时效", "scope": "工作日预约的仓库上门揽收",
        "facts": [
            ('预约截止', '当地时间15:00前预约可安排当日揽收'),
            ('等待时长', '司机到场后最长等待20分钟'),
            ('异常代码', 'PICKUP-NOSHOW-20'),
        ],
    },
    {
        "id": "return_warehouse", "title": "退货仓路由", "scope": "US站买家退回的普通商品",
        "facts": [
            ('路由原则', '按买家邮编路由至最近的启用退货仓'),
            ('不可售商品', '危险品和破损液体不得寄入普通退货仓'),
            ('路由代码', 'RETURN-ROUTE-NEAREST'),
        ],
    },
    {
        "id": "coupon_refund", "title": "优惠券退款", "scope": "使用平台优惠券支付的取消订单",
        "facts": [
            ('返还条件', '订单全额取消且优惠券未过期时原券退回'),
            ('部分退款', '部分退款不返还优惠券面额'),
            ('流水类型', 'COUPON-RETURN'),
        ],
    },
    {
        "id": "marketplace_payout", "title": "卖家提现", "scope": "已完成风控校验的卖家余额提现",
        "facts": [
            ('处理周期', '工作日提交后2个工作日内出款'),
            ('最低金额', '单次提现金额不得低于25 USD'),
            ('批次前缀', 'PAYOUT-BATCH-2D'),
        ],
    },
    {
        "id": "fraud_review", "title": "高风险订单复核", "scope": "命中支付风险规则的订单",
        "facts": [
            ('自动放行阈值', '风险分低于35时允许自动放行'),
            ('人工时限', '风险分35及以上须在6小时内人工复核'),
            ('队列名称', 'FRAUD-MANUAL-6H'),
        ],
    },
    {
        "id": "account_verification", "title": "卖家身份验证", "scope": "新注册跨境卖家账户",
        "facts": [
            ('提交期限', '注册后7个自然日内完成身份材料提交'),
            ('受益人要求', '持股25%及以上的最终受益人均需验证'),
            ('状态代码', 'KYC-PENDING-7D'),
        ],
    },
    {
        "id": "product_recall", "title": "商品召回处理", "scope": "已发布强制召回通知的商品",
        "facts": [
            ('下架时限', '召回通知发布后2小时内完成全站下架'),
            ('在途处理', '未交付订单必须拦截并全额退款'),
            ('事件代码', 'RECALL-STOP-2H'),
        ],
    },
    {
        "id": "listing_compliance", "title": "商品标题合规", "scope": "新建或更新的商品Listing",
        "facts": [
            ('标题长度', '标题最多允许160个字符'),
            ('禁用内容', '标题禁止包含联系方式和绝对化宣传词'),
            ('拦截代码', 'LISTING-TITLE-160'),
        ],
    },
    {
        "id": "preorder_deposit", "title": "预售定金", "scope": "采用定金加尾款模式的预售订单",
        "facts": [
            ('定金比例', '定金不得超过商品实付总额的20%'),
            ('尾款期限', '尾款通知后72小时内完成支付'),
            ('关闭代码', 'DEPOSIT-BALANCE-72H'),
        ],
    },
    {
        "id": "warehouse_cycle_count", "title": "仓库循环盘点", "scope": "高价值SKU的周期性库存盘点",
        "facts": [
            ('盘点频率', '高价值SKU每7天至少盘点一次'),
            ('差异冻结', '账实差异超过2件时立即冻结出库'),
            ('任务前缀', 'CYCLE-COUNT-7D'),
        ],
    },
    {
        "id": "carrier_claim", "title": "承运商索赔", "scope": "运输破损或丢失的承运商责任案件",
        "facts": [
            ('提交期限', '签收或丢件认定后10个自然日内提交'),
            ('证据要求', '必须同时提供运单、货值凭证和损坏照片'),
            ('案件前缀', 'CARRIER-CLAIM-10D'),
        ],
    },
    {
        "id": "tax_withholding", "title": "平台代扣税", "scope": "适用平台代扣规则的US站订单",
        "facts": [
            ('计税时点', '以订单完成时的应税销售额计算'),
            ('退款调整', '全额退款后在下一结算周期冲回代扣税'),
            ('账务代码', 'TAX-WITHHOLD-REVERSAL'),
        ],
    },
    {
        "id": "delivery_signature", "title": "高价值包裹签收", "scope": "申报价值较高的跨境包裹",
        "facts": [
            ('签名阈值', '申报价值达到300 USD必须购买签名服务'),
            ('代签限制', '高价值包裹不得由门卫无姓名代签'),
            ('服务代码', 'SIGNATURE-300'),
        ],
    },
    {
        "id": "restricted_goods", "title": "受限商品审核", "scope": "包含液体、粉末或磁性材料的商品",
        "facts": [
            ('审核要求', '发布前必须完成运输属性预审'),
            ('文件有效期', '运输鉴定报告有效期为12个月'),
            ('审核代码', 'DG-PRECHECK-12M'),
        ],
    },
    {
        "id": "payment_dispute", "title": "支付争议预警", "scope": "买家发起但尚未升级为拒付的支付争议",
        "facts": [
            ('响应期限', '收到预警后48小时内首次响应'),
            ('退款权限', '超过100 USD的主动退款必须人工复核'),
            ('工单前缀', 'DISPUTE-EARLY-48H'),
        ],
    },
    {
        "id": "inventory_aging", "title": "库龄库存处理", "scope": "海外仓长期未售出的普通库存",
        "facts": [
            ('长库龄阈值', '入库满180天进入长库龄管理'),
            ('处置通知', '销毁或清退前至少提前30天通知卖家'),
            ('状态代码', 'AGED-INVENTORY-180D'),
        ],
    },
    {
        "id": "packaging_recycling", "title": "包装回收标识", "scope": "销往德国的商品外包装",
        "facts": [
            ('注册要求', '发货前必须登记LUCID包装注册号'),
            ('标签位置', '回收标识必须清晰印在外包装可见面'),
            ('校验代码', 'DE-PACK-LUCID'),
        ],
    },
    {
        "id": "export_control", "title": "出口管制筛查", "scope": "跨境订单的收件人与最终目的地",
        "facts": [
            ('筛查时点', '付款成功后、仓库放行前完成筛查'),
            ('命中处理', '名单疑似命中必须冻结并转人工复核'),
            ('冻结代码', 'EXPORT-SCREEN-HOLD'),
        ],
    },
    {
        "id": "delivery_attempt", "title": "末端派送尝试", "scope": "需要收件人签收的普通跨境包裹",
        "facts": [
            ('尝试次数', '至少完成2次派送尝试后才可退回'),
            ('间隔要求', '两次派送尝试至少间隔24小时'),
            ('退回代码', 'DELIVERY-RETURN-2X'),
        ],
    },
    {
        "id": "seller_vacation", "title": "卖家休假模式", "scope": "启用Vacation Mode的自发货卖家",
        "facts": [
            ('生效延迟', '开启后最长15分钟停止新订单进入'),
            ('存量订单', '开启前已付款订单仍须按原SLA履约'),
            ('状态代码', 'SELLER-VACATION-ACTIVE'),
        ],
    },
]

ALL_POLICIES = BASE.POLICIES + TEST_POLICY_DEFINITIONS

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
}

QUERY_BANK.update({
    "vat_invoice": [
        (0, "scenario", "去年完成的欧区企业订单，过了三个月还能补开VAT发票吗？"),
        (1, "implicit", "买家填了欧洲企业税号，平台应到哪个系统确认它有效？"),
        (2, "exact_code", "EU B2B发票流程在系统里对应哪个业务编码？"),
        (0, "colloquial", "公司客户成交后最晚隔多久还能要税票？"),
    ],
    "address_change": [
        (0, "scenario", "订单已经进入拣货环节，买家还能在页面自行换收货地址吗？"),
        (1, "scenario", "客户想把法国地址改成德国地址，能否直接覆盖原订单？"),
        (2, "exact_code", "记录新版收货地址修改时应该写入哪个事件名？"),
        (0, "colloquial", "仓库还没开始捡货时改地址来得及吗？"),
    ],
    "lost_parcel": [
        (0, "paraphrase", "跨境件物流轨迹停了几个自然日后可以启动丢失调查？"),
        (1, "implicit", "包裹确认遗失后能不能按吊牌价的两倍赔给客户？"),
        (2, "exact_code", "十四天无轨迹的丢件案件使用什么case code？"),
        (0, "colloquial", "物流两周一点没动，可以报丢了吗？"),
    ],
    "customs_declaration": [
        (0, "scenario", "买家用了折扣券，出口申报写原价还是实际支付的商品金额？"),
        (1, "implicit", "商业包裹英文品名全部填gift能通过当前报关规则吗？"),
        (2, "exact_code", "报关金额的currency字段应填$符号还是三位字母？"),
        (0, "colloquial", "海关申报能按建议零售价写吗？"),
    ],
    "oversize_shipping": [
        (0, "scenario", "纸箱最长一边是125厘米，应不应该进入大件运输流程？"),
        (1, "implicit", "体积很大但实际很轻的包裹，承运费只看秤重吗？"),
        (2, "exact_code", "识别为bulky shipment后应下发哪个服务标识？"),
        (0, "colloquial", "一米二以上的箱子就算超大件了吗？"),
    ],
    "pickup_sla": [
        (0, "scenario", "仓库在当地下午四点才预约，上门取件还能安排在今天吗？"),
        (1, "paraphrase", "司机已经抵达仓门，等待交货的最长时间是多少分钟？"),
        (2, "exact_code", "司机等待超时仍未交货，应登记哪个异常原因？"),
        (0, "colloquial", "想当天揽收，预约最晚别超过几点？"),
    ],
    "return_warehouse": [
        (0, "implicit", "US客户申请退货时，系统根据什么把他分到具体退货仓？"),
        (1, "scenario", "包装已破且正在漏液的商品可以寄进普通售后仓吗？"),
        (2, "exact_code", "按距离选择退货仓的规则标识是什么？"),
        (0, "colloquial", "退货是不是不管客户在哪都发到西部仓？"),
    ],
    "coupon_refund": [
        (0, "scenario", "整单取消时优惠券仍在有效期，用户账户会重新收到原券吗？"),
        (1, "implicit", "只退订单中一件商品时，优惠券面额会按比例返还吗？"),
        (2, "exact_code", "原券回到账户时生成的流水类型叫什么？"),
        (0, "colloquial", "券没过期、订单全退，券会不会一起回来？"),
    ],
    "marketplace_payout": [
        (0, "paraphrase", "卖家周一工作日申请提取余额，平台承诺几个工作日完成出款？"),
        (1, "scenario", "账户里只有20美元时能否发起一笔提现？"),
        (2, "exact_code", "两日出款任务创建的batch id应采用什么前缀？"),
        (0, "colloquial", "审核都过了以后提现还要等一周吗？"),
    ],
    "fraud_review": [
        (0, "scenario", "支付风险评分为34的订单是否可以由系统直接放行？"),
        (1, "paraphrase", "进入人工风控的订单，要求在多少小时内给出结论？"),
        (2, "exact_code", "风险分达到人工线后任务会投递到哪个queue？"),
        (0, "colloquial", "风控分35分还能自动过吗？"),
    ],
    "account_verification": [
        (0, "paraphrase", "跨境卖家注册完成后，身份资料最迟第几天要提交？"),
        (1, "scenario", "某自然人持股30%，是否必须纳入最终受益人身份核验？"),
        (2, "exact_code", "注册后尚未交KYC材料的账户应该标记什么状态？"),
        (0, "colloquial", "新店开了一个月再补身份认证行不行？"),
    ],
    "product_recall": [
        (0, "scenario", "监管召回通知已发布三小时，商品仍在售是否已经违反处理时限？"),
        (1, "implicit", "被强制召回的商品已发出但未签收，是继续送还是拦截退款？"),
        (2, "exact_code", "全站紧急停止销售的召回事件使用哪个code？"),
        (0, "colloquial", "召回消息来了以后最晚几个小时得全部下架？"),
    ],
    "listing_compliance": [
        (0, "paraphrase", "新商品Listing标题的字符数量上限是多少？"),
        (1, "scenario", "卖家想在商品标题里留下客服邮箱，当前规则允许吗？"),
        (2, "exact_code", "标题长度超过限制时接口返回哪个拦截码？"),
        (0, "colloquial", "商品标题写到两百字会被挡住吗？"),
    ],
    "preorder_deposit": [
        (0, "scenario", "一件实付100美元的预售商品，可以先收30美元定金吗？"),
        (1, "paraphrase", "平台发出补尾款通知后，买家有多少小时完成支付？"),
        (2, "exact_code", "超过尾款窗口关闭预售单时使用什么状态代码？"),
        (0, "colloquial", "预售先收一半的钱符合定金规则吗？"),
    ],
    "warehouse_cycle_count": [
        (0, "implicit", "高价值货品一个月只盘点一次是否满足当前频率要求？"),
        (1, "scenario", "系统库存比实物多3件，仓库是否需要马上冻结出库？"),
        (2, "exact_code", "每周高价值SKU盘点任务编号采用什么前缀？"),
        (0, "colloquial", "贵重SKU是不是每星期都得盘一次？"),
    ],
    "carrier_claim": [
        (0, "paraphrase", "确认物流破损责任后，索赔材料最晚多少个自然日送达承运商？"),
        (1, "scenario", "申请运输赔付时只有tracking number够不够，还必须补什么？"),
        (2, "exact_code", "十日内提交的物流索赔case id以什么开头？"),
        (0, "colloquial", "丢件确认两周以后再找承运商赔还来得及吗？"),
    ],
    "tax_withholding": [
        (0, "implicit", "US平台代扣税应按下单标价，还是等订单完成后按应税销售额算？"),
        (1, "scenario", "订单已经全额退款，之前平台扣的税会在哪个周期调整回来？"),
        (2, "exact_code", "下一账期冲回代扣税时应写入哪个账务类型？"),
        (0, "colloquial", "订单没完成时就把代扣税最终锁死对吗？"),
    ],
    "delivery_signature": [
        (0, "scenario", "申报价值正好300美元的包裹是否必须加购签名签收？"),
        (1, "implicit", "高价值件只记录“门卫代收”但没有姓名，可以视为有效交付吗？"),
        (2, "exact_code", "达到货值门槛后应向承运商传递哪个签名服务代码？"),
        (0, "colloquial", "三百美金的货能直接放门口不签字吗？"),
    ],
    "restricted_goods": [
        (0, "scenario", "含磁性材料的新品能否先发布销售，之后再补运输属性审核？"),
        (1, "paraphrase", "液体商品的运输鉴定报告经过一年后还能继续使用吗？"),
        (2, "exact_code", "危险属性商品发布前检查对应哪个审核标识？"),
        (0, "colloquial", "粉末类商品是不是上架前就得先做运输预审？"),
    ],
    "payment_dispute": [
        (0, "paraphrase", "平台收到支付争议预警后，第一次回复最迟不能超过多少小时？"),
        (1, "scenario", "一笔150美元的争议订单能否由系统直接主动退款？"),
        (2, "exact_code", "早期支付争议创建工单时采用什么编号前缀？"),
        (0, "colloquial", "争议预警拖三天才回应是不是超时了？"),
    ],
    "inventory_aging": [
        (0, "scenario", "某批海外仓商品入库已经200天，应不应该进入长库龄管理？"),
        (1, "paraphrase", "平台准备销毁滞销库存时，至少提前多少天告诉卖家？"),
        (2, "exact_code", "库存达到半年库龄后应打上哪个状态标识？"),
        (0, "colloquial", "货在仓里放半年还没卖掉算长库龄吗？"),
    ],
    "packaging_recycling": [
        (0, "implicit", "商品准备发往德国，包装注册号可以等产生销量以后再补吗？"),
        (1, "scenario", "只在电子说明书里展示回收图标，外箱没有标识是否合规？"),
        (2, "exact_code", "德国包装注册检查在系统中对应哪个校验码？"),
        (0, "colloquial", "寄德国之前是不是得先有LUCID号？"),
    ],
    "export_control": [
        (0, "paraphrase", "跨境订单的制裁名单检查是在付款后、出库前哪个阶段完成？"),
        (1, "scenario", "收件人姓名与管制名单疑似匹配，仓库还能继续放行吗？"),
        (2, "exact_code", "出口筛查转人工冻结时写入什么业务代码？"),
        (0, "colloquial", "货都出境了再补名单筛查可以吗？"),
    ],
    "delivery_attempt": [
        (0, "scenario", "需要签收的包裹第一次无人接收后，承运商可以立即退回吗？"),
        (1, "paraphrase", "末端第二次上门与第一次之间至少需要相隔多少小时？"),
        (2, "exact_code", "完成两次派送仍失败后应记录哪个退回原因？"),
        (0, "colloquial", "派了两次都没人收才能退件，对吗？"),
    ],
    "seller_vacation": [
        (0, "scenario", "卖家打开休假开关后，半小时内是否还应该持续进入新订单？"),
        (1, "implicit", "Vacation Mode开启前已经付款的订单会被平台自动取消吗？"),
        (2, "exact_code", "自发货店铺休假模式正式生效时使用什么状态？"),
        (0, "colloquial", "开休假以后原来欠着的单还得按时发吗？"),
    ],
})

TEST_POLICIES = {policy["id"] for policy in TEST_POLICY_DEFINITIONS}

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


def long_policy_document(policy: dict) -> str:
    version = "2026.08 当前生效版"
    state = "当前有效"
    facts = policy["facts"]
    values = [value for _, value in facts]
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
    if set(QUERY_BANK) != {p["id"] for p in ALL_POLICIES}:
        raise RuntimeError("query bank and policy IDs differ")
    if len({p["id"] for p in ALL_POLICIES}) != len(ALL_POLICIES):
        raise RuntimeError("duplicate policy ID")
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir(parents=True)

    business = []
    cases = []
    for policy in ALL_POLICIES:
        doc_id = policy["id"]
        document_text = long_policy_document(policy)
        business.append({"id": doc_id, "tier": "policy", "text": document_text})
        split = "test" if policy["id"] in TEST_POLICIES else "dev"
        for number, (fact_index, category, query) in enumerate(QUERY_BANK[policy["id"]], start=1):
            gold_span = policy["facts"][fact_index][1]
            if gold_span not in document_text:
                raise RuntimeError(f"gold span missing: {policy['id']} #{number}")
            cases.append({
                "id": f"long_{policy['id']}_{number}",
                "policy_id": policy["id"],
                "split": split,
                "category": "boundary" if fact_index == 1 else category,
                "query": query,
                "gold_docs": [doc_id],
                "gold_span": gold_span,
            })

    if len({case["id"] for case in cases}) != len(cases):
        raise RuntimeError("duplicate case ID")
    normalized_queries = [case["query"].strip().casefold() for case in cases]
    if len(set(normalized_queries)) != len(normalized_queries):
        raise RuntimeError("duplicate query text")
    dev_count = sum(case["split"] == "dev" for case in cases)
    test_count = sum(case["split"] == "test" for case in cases)
    if (dev_count, test_count) != (40, 100):
        raise RuntimeError(f"unexpected split sizes: dev={dev_count} test={test_count}")

    distractors = [
        {"id": f"domain_distractor_{i:02d}", "tier": "domain_distractor", "text": long_distractor(i, p)}
        for i, p in enumerate(ALL_POLICIES)
    ]
    background = [
        {"id": f"background_{i:03d}", "tier": "background", "text": long_background(i)}
        for i in range(120)
    ]
    core = business + distractors
    large = core + background
    write_subset("core", core)
    write_subset("noise120", large)
    CASES.write_text("".join(json.dumps(c, ensure_ascii=False) + "\n" for c in cases), encoding="utf-8")

    lengths = [len(doc["text"]) for doc in large]
    manifest = {
        "synthetic": True,
        "description": "固定RAG检索评测集；文档与查询独立构造，事实均为虚构。",
        "query_authorship": "independent manually-authored query bank; not document template questions",
        "split": "policy-level holdout: 10 dev policies / 25 test policies",
        "subsets": {"core": len(core), "noise120": len(large)},
        "tiers": {
            "policy": len(ALL_POLICIES),
            "domain_distractor": len(ALL_POLICIES),
            "background": 120,
        },
        "queries": len(cases),
        "dev_queries": sum(c["split"] == "dev" for c in cases),
        "test_queries": sum(c["split"] == "test" for c in cases),
        "document_chars": {"min": min(lengths), "max": max(lengths), "mean": round(sum(lengths) / len(lengths), 1)},
    }
    MANIFEST.write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(manifest, ensure_ascii=False))


if __name__ == "__main__":
    main()
