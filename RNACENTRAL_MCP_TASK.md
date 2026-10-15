# HYB2 × RNAcentral MCP：第一版接入开发任务书

日期：2026-10-14
状态：接入设计；不是已合并补丁，也不是成功联调记录。
范围：公开 RNA 标识符、已有文献摘要、注释核对与报告来源管理。不包含原始数据上传、序列检索、结构优化或自动科学结论。

> 本文件是用户提供的任务书（2026-10-14）的整理稿：保留全部要求与完成标准，省略了核查来源链接。实现接口见 `desktop/spec/EVIDENCE.md`；
> 2026-10-15 的真实联调结果记录在该文件的“Verified upstream behaviour”一节。

## 1. 产品验收目标

用户在 HYB2 当前结果旁，确认公开 RNA 标识符及物种，点击“查看相关研究”，可以取得 RNAcentral 已有的文献概要、查看来源、保存一张背景资料卡，并把它加入当前项目报告。无需自行安装 Claude Desktop、编写 MCP 配置或使用终端。

没有摘要、网络错误和没有直接结构证据必须分别表达。默认把外部摘要归类为背景材料，不作为当前预测的验证。

第一版由按钮直接调用确定工具，不以新接入语言模型为前提。以后增加问答时复用相同能力和证据记录。

## 2. 本次核查的实际基线

HYB2：核查分支 feature/desktop-hardening-python-pipeline。desktop/README.md 说明新桌面入口为 Go/MyGo + bundled Python。当前 Runtime 有 control 和 compute 两条 Python sidecar；bridge.py 接收 HYB2 自定义逐行 JSON 协议，不是 MCP 协议。

RNAcentral：官方项目为 stdio MCP server，安装入口 run-server；PyPI 版本 0.1.1；源码使用 mcp.server.fastmcp.FastMCP，依赖只写 mcp[cli]>=1.8.0；MCP SDK v2 迁移说明确认该导入发生破坏性变化。第一版应锁定经过测试的 v1 SDK 及依赖（开发约束 mcp[cli]>=1.8,<2，再生成精确版本与哈希锁文件）。

## 3. 架构决定

HYB2 是外部 MCP 的客户端宿主；不把 HYB2 自身改成 MCP server。通信链：Go/MyGo 页面 → Go EvidenceService（权限、超时、取消、任务状态和本地证据记录）→ 独立 Python evidence sidecar（官方 MCP SDK）→ 固定的 RNAcentral MCP 子进程（stdio）→ RNAcentral 网络 API。control / compute 不承载等待外部网络的工作。证据进程按需启动，退出应用时回收。禁止把原有 bridge 协议直接当作 MCP。首版不增加远程 HTTP MCP 网关。

## 4. 工具能力策略

- P0 get_rna_description：读取 RNAcentral 已有文献概要；输出原文摘要、取回时间、编号、服务与适配器版本、结果状态；未知字段保持 null/unknown。
- P0/P1 map_rna_id：仅辅助核对公开身份，不自动决定样本、物种、参考或结构对应；要求明确 taxon 并校验返回物种；只展示映射结果，请用户确认。
- P1 query_rnacentral：只用于用户明确要求的公开记录查询，限制参数、数量与响应大小。
- 暂不启用：序列搜索、批量序列导出、坐标自动推断、计算设置修改；数据库结构图片不作为交付依赖。工具发现不等于自动授权。

## 5. LitScan 不等于一个已存在的 MCP 工具

server.py 中没有独立 LitScan 原句/全文检索工具。第一版提供公共来源链接；内嵌原句与结构化论文信息作为单独连接器任务。

## 6. 证据对象与范围

EvidenceCard 字段：schema_version、evidence_id；source_provider、source_tool、adapter_version、server_package_version；query_identifier、taxon、identity_confirmation；requested_at、retrieved_at；status（success / summary_unavailable / upstream_error / timeout / cancelled / identity_unconfirmed / unexpected_response）；content_kind；raw_response_digest、raw_response_local_ref、display_text；source_identifiers；citation_verification；relation_to_current_structure；review（不能由模型自行设为已审核）。检索记录与当前计算结果的绑定独立保存（project_id、run_id、structure_id、record/window、result_digest），不发送到上游。切换候选或结果不能自动沿用旧说明。

## 7. 当前上游特殊错误需要适配

literature.py 把异常返回为普通字符串 "Error querying RNAcentral API: ..."，也可能返回 "No description available"。处理顺序：本地超时/取消/协议错误 → MCP 工具错误 → 固定版本的已知错误文本与无摘要文本 → 空值/非文本/异常格式 → 正常文本作为 external_ai_summary。404 不能未经验证转为“没有摘要”，查不到摘要更不能转为“没人研究过”。

## 8. 用户界面

Structure：“相关研究”区域（公开编号 + 物种 + 身份确认状态；发送字段说明；查看已有摘要 / 核对编号 / 打开来源；卡片标示“外部 AI 文献概要；不是当前结构的验证”；加入报告由用户显式执行）。Preferences：是否允许外部公开资料查询、连接状态与测试、缓存清理、脱敏日志。Report：“外部资料与研究者备注”章节，标记来源、检索日期、结果绑定及审核状态；没有引用时说明来源未核实；不修改计算方法、参数或数值。

## 9. 文件级开发任务

新增：desktop/app/core/evidence.go、model/evidence.go、views/evidence.go；desktop/runtime/evidence/worker.py、mcp_client.py、rnacentral.py、store.py；对应测试。修改：core/service.go（Evidence 领域方法，不增加任意 tool/arguments/command 的通用入口）、core/runtime.go、views/structure.go、views/report.go、desktop/spec、native staging / verify scripts。

## 10. 安全与数据边界

默认不上传 RAW、FASTQ、HYB、私人序列、结构数据、绝对路径、样本分组或项目文件；首次启用外部查询明确告知；MCP 子进程使用固定程序路径与允许的环境变量，禁止 shell 拼接；不向子进程开放项目目录或凭证；外部文本始终作为数据；新增 LLM 时再次区分授权；禁止无限重试，尊重限流。

## 11. 完成标准

A. 本机真实初始化、tools/list、get_rna_description 至少一次成功；记录版本与返回。
B. 干净机器无需系统 Python、uv、终端或 Claude Desktop。
C. 公共非致病性示例覆盖有摘要、无摘要、错误编号、身份歧义、错误物种。
D. 覆盖 DNS 失败、超时、限流、断网、异常返回、取消与退出重开。
E. 外部错误不会阻塞分析、不冒充零命中、不写入正常摘要。
F. 网络观察确认只发送已授权字段。
G. 结果/候选切换不混用旧证据绑定。
H. 报告导出保留来源、日期、审核状态和证据边界。
I. 自动问答不作为 P0 交付依赖。
