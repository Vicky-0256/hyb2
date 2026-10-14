# HYB2 Desktop：从原始数据到结构预测的重新规划

日期：2026-10-14。基准数据：`../zika_files`（119 GB），参照结果：`../reproduce_zikv`（用命令行 hyb2 跑出的 14 个样本、比较和全基因组 CPLfold）。

## 1. 目标

APP 要让用户在一台普通工作站上，**不碰命令行**，完成 reproduce_zikv 用脚本做的全部工作：

```
原始 FASTQ(.gz) / collapsed FASTA(.gz)  ×  多个样本（重复、条件）
  → Bowtie2 比对 → .hyb（每个样本）
  → contact map / 统计 / 质控
  → 条件之间的差异比较
  → 用全部嵌合体做证据的结构预测：全基因组（约 10.8 kb）+ 局部窗口（可含假结）
```

### 验收指标（以 zika_files 为准，参照机：M 系列 10 核 / 32 GB）

| # | 指标 | 目标 |
|---|---|---|
| G1 | 单个最大样本（ZBRA_REP2，13.5 GB FASTQ，约 4400 万 reads）跑到 .hyb | 峰值内存 ≤ 4 GB，且与数据量无关；用时 ≤ 20 min，以 Bowtie2 为瓶颈 |
| G2 | 结果正确 | 现有 goldens 仍逐字节一致；14 个样本与 `results_combined/*.hyb` 的杂交体数和 contact map 一致（只有已记录的有意差异，contact map 相关性 ≥ 0.999） |
| G3 | 批量 | 一张样本表把 14 个样本一次排队；APP 退出或崩溃后，从最后完成的阶段继续 |
| G4 | 分析大文件 | 1.5 GB 的 .hyb（ZSIN_REP3，640 万个杂交体）能在 APP 里打开，contact map 和筛选的响应 ≤ 1 s |
| G5 | 全基因组结构 | 10.8 kb 的不含假结预测，以全部嵌合体为证据，≤ 3 min（参照 `cplfold_genome.py`：建矩阵 16 s + 折叠 93 s） |
| G6 | 用满 CPU | 多个局部窗口、多个样本的折叠并行执行，CPU 占用接近核数；同时运行的任务受内存预算限制 |
| G7 | 差异比较 | 两个条件之间的差异 bin 和合并后的相互作用，与 `reproduce_zikv/compare_*` 一致（padj 的秩相关 ≥ 0.99） |

## 2. 现状与差距（已实测）

### 2.1 Pipeline 把所有数据放在内存里：这是首要问题

用 ZAFR_REP1 开头的数据，在 APP 自带的 runtime 里跑 `python -m hyb2pipe`：

| reads | 用时 | 峰值内存 | SAM |
|---|---|---|---|
| 200 万 | 65 s | 1.8 GB | 0.64 GB |
| 800 万 | 258 s | **7.1 GB** | 2.45 GB |
| 3290 万（整个 ZAFR_REP1，按线性外推） | ≈ 18 min | **≈ 29 GB** | ≈ 10 GB |

800 万 reads 时各阶段用时：去重 43 s（单线程），Bowtie2 130 s（多核），sam2blast 34 s，排序和排名 36 s，杂交体识别 13 s。

原因在代码里：`inputs.fastq_to_rows` 和 `collapse_reads` 把全部 reads 读成 list；`sam2hyb` 把全部比对行读成 `blast_lines` list，还要把 SAM 读两遍；`collapse_*` 和 `awk.sort_lines_by_field` 都在内存里排序。所以内存跟数据量成正比，在 32 GB 的机器上跑大样本会大量用 swap，甚至被系统杀掉。ZSIN_REP3 有 640 万个杂交体，下游阶段的压力更大。

### 2.2 输入管理不适合 GB 级文件

- `project.import_input` 会把文件**复制**进 `inputs/`，再计算两遍 sha256。整个过程在控制 sidecar 里同步执行，而且持有 manifest 锁。导入一个 13 GB 的 FASTQ，要多占 13 GB 磁盘，UI 会卡住好几分钟。
- 格式按后缀判断，`.fastq.gz` 和 `.fasta.gz` 都被识别成 `other`。
- 没有“样本”这个概念：缺少条件、重复、参考序列、目标基因 ID（相当于 hyb2 的 `-a`）这些字段，也没有样本表。
- 每个样本会留下约 10 GB 的 SAM，14 个样本就是 130 GB，而本机只剩 226 GB 可用空间。目前也不会预先检查磁盘空间。

### 2.3 长任务不能续跑

任务是一次性的子进程。APP 退出时（`RunEvent::Exit → shutdown`）任务会被杀掉，重新开始只能从头跑。整个流程要跑几十分钟到几小时，必须能按阶段续跑。

### 2.4 分析页处理不了真实规模的 .hyb

- `project.read_input` 有 50 MB 的上限。ZIKV 的 .hyb 在 60 MB 到 1.56 GB 之间，几乎全部超限。
- `web/parser.worker.js` 会把每条记录都 `push` 进 `summary.records`，再整体交给主线程。几百万个 JS 对象会让 WebView 卡死。
- hyb2 下游的命令一个都还没接入 APP：`hyb2_coverage`（contact.txt / forTable）、viewpoint、`hyb2_compare`（R + DESeq2），以及用嵌合体约束做结构的 bubble 流程。

### 2.5 结构预测受人为限制

- `HYB2_CPLFOLD_MAX_NT=1000` 是写死的。Outside pass 已经用 Numba 编译，1000 nt 只要约 3 s。reproduce_zikv 用同一份 CPLfold，以不含假结模式折叠 10.8 kb 只要约 2 min。
- `cplfold_web.build_hyb_bonus_matrix` 会对全部 arm 做稠密的 N×N 高斯运算。`analysis/cplfold_genome.py` 已经有一个稀疏版本（Numba，只算 ±5.3σ 以内），与稠密版的误差 ≤ 1.6e-5，可以直接移植。
- 一个 compute sidecar 只能串行执行，没有多窗口、多样本并行折叠的任务类型。
- 10.8 kb 时含假结的 Phase 2 有多快还没测过，需要先量。

## 3. 架构调整

### 3.1 数据模型：项目 → 样本 → 运行

```
project.json
  references[]   {id, path, gene_ids[], sha256}
  samples[]      {id, name, condition, replicate, reads: {path, mode: "linked"|"copied", size, mtime, quick_hash},
                  reference_id, gene_id, settings}
  runs[]         每个样本每个阶段的状态和产物（hyb、contact、stats……）
  comparisons[]  {condition_one, condition_two, samples, results}
  structures[]   {sample(s), gene_id, window|genome, mode, results}
```

- 原始数据默认**原地引用**：记录路径、大小、mtime，以及首尾各 1 MB 的快速指纹。完整 sha256 放到后台任务里算。复制改成可选项。项目打开时先校验文件，文件被移动或修改后提示重新定位。
- 样本表可以从 TSV/CSV 导入，列与 `ZIKV_dataset_map.tsv` 和 `compare_inputs.tsv` 对齐。

### 3.2 流式 pipeline：内存不随数据量增长

每个阶段都读一个文件、写一个文件，用有界内存的外部排序（分块排序后用 `heapq.merge` 归并，排序键和比较规则与现在的 `awk.sort_*` 完全一致），从而保持逐字节一致：

1. **读取和去重**：流式解压，按序列做外部排序后计数（相当于 `make_comp_fasta.pl`）。按计数排名也走外部排序。可以按序列的哈希分桶，用多个进程并行处理。
2. **比对**：Bowtie2 的 stdout 直接经过管道进入 `sam_filt → sam2blast`，同时只抽出 `attach_sequences` 要用的 read 序列，写成小的 sidecar 文件。默认不落盘 SAM；需要时可以选“保留 SAM（gzip）”。
3. **杂交体识别**：用了 `--reorder` 后，同一条 read 的比对是连续的，所以 `mtop_hybrids` 可以按 read 分组流式处理，每次只在内存里放一条 read 的比对。reads 可以分块交给进程池；排名需要的全局计数先做一次累加，再在第二遍里使用。
4. **去冗余、注释、能量**：外部排序加流式处理；`-e` 继续用已有的多进程 ViennaRNA。

每个阶段完成后写一个 checkpoint（产物路径、大小和输入指纹），续跑时从第一个未完成的阶段开始。

### 3.3 分析后端：WebView 只拿聚合结果

每个 .hyb 生成后，Python 在后台建一个**分析索引**：

- `contact.txt` 和 forTable：移植 `hyb2_coverage`，与命令行逐字节一致，是 contact map 的唯一数据源。
- 一个 SQLite 文件（或列式 `.npz`），存每个杂交体的坐标、计数、dG 和类型，并建坐标索引，支持区域查询和条件筛选。
- 新增 bridge 操作：`analysis_summary`、`contact_bins(gene, bin, region)`、`hybrids_query(filters, limit)`、`top_interactions`。WebView 只接收这些聚合结果（10.8 kb 在 10 nt bin 下约 117 万个格子，按需降采样）。
- 网页版继续用浏览器内的解析器处理小文件；桌面版看到大文件时改走后端，不再受 50 MB 上限限制。

### 3.4 结构预测：长度上限按内存估算，按核数并行

- 去掉固定的 1000 nt 上限，改成按 `N² × 4 B` 的 bonus 矩阵加上 parser 表估算内存，再结合可用内存给出允许长度；超出时提示改用窗口模式。
- 移植稀疏的 bonus 矩阵构建（Numba），直接从分析索引里读出全部 arm，不再要求 WebView 先把 arm 传下来。
- 新的任务类型 `structure_batch`：
  - `genome`：整条 RNA 的不含假结预测（Phase 1 only）；
  - `windows`：对选中的区域或差异相互作用（参照 `select_regions_cplfold.py`）分别做含假结的预测；
  - 用进程池执行，每个进程跑一个窗口，`numba.set_num_threads(1)`，并发数取 `min(核数, 内存预算 / 单个窗口的估算内存)`。
- 结果统一存成 dbn、pairs.tsv 和 ct，并记录参数和证据摘要；配一个能显示 10 kb 弧图的查看器。

### 3.5 任务调度

- Rust 继续是唯一写 status 的一方，调度器增加 **CPU 与内存预算**：Bowtie2 `-p` 和折叠并发数从同一个预算里分配，同时运行的样本数受内存限制。
- 进度上报改成“已处理字节 / 总字节”加 ETA，不再只有阶段名。
- 正在跑任务时关闭窗口要提示用户；退出时把任务标成 `interrupted`，下次打开后可以一键续跑。
- 任务启动前先检查磁盘空间：按输入大小估算中间文件需要的空间。

### 3.6 差异比较

`hyb2_compare` 依赖 R 和 DESeq2，reproduce_zikv 在本机还遇到了 dyld 的问题。**建议**：bin 计数和合并相互作用用 Python 移植；统计部分用 PyDESeq2，再用 7 组 `compare_*` 的结果验证（G7）。备选方案是在桌面包里带一个精简的 R 环境，但包体积和跨平台成本都高得多。

**实施结果（2026-10-14）**：PyDESeq2 依赖 scipy、pandas、anndata、scikit-learn 和 matplotlib，捆绑运行时只有 numpy 和 numba，所以改为把 DESeq2 1.46 两条件 Wald 检验的每一步直接移植到 numpy（`pipeline/hyb2analysis/deseq.py`），按 R 和 C++ 源码逐项对齐，包括 locfit、loess 和 lowess 的细节。和 `reproduce_zikv` 的 14 组 `compare_*` 逐项对比：13 组显著 bin 完全一致或只差 1 到 3 个，padj 秩相关为 1.0；min30 一组是 0.9994，原因是 R 在残差自由度 ≤ 3 时用 1e4 次随机抽样估计先验方差，这里用的是精确分布。合并后的 interaction 与 legacy 一致。每组约 20 秒，峰值内存约 2.4 GB。桌面版以 `compare` 任务运行，结果登记在 `comparisons[]` 中，比较页显示 padj 加权的显著 bin 图和合并 interaction 列表，可以直接定位、打开 Region 或做 A/B 两次折叠。

### 3.7 整体流程与界面：一个项目、一条主线

#### 现在为什么显得割裂

现在的 APP 其实是两个应用叠在一起：桌面壳（`desktop/ui`）和嵌进去的整个网页版（`web/app.js`）。

| 现象 | 代码位置 |
|---|---|
| 两套导航：桌面侧栏（Project Home / Analysis / Task Center / Runtime），点进 Analysis 后又出现网页版自己的侧栏（Overview…Structure，外加 Validation / Methods / File format / Privacy） | `desktop/ui/index.html:16`、`web/app.js:57,527` |
| 两套数据入口：项目在 Project Home 导入文件，分析页仍有自己的落地页，可以从浏览器“上传”文件；Compare 页的数据集也是逐个选文件加进来的，跟项目无关 | `web/app.js:368,1539` |
| 分析页一次只认一个 .hyb 和一个 FASTA：`syncProjectInputs` 取每种角色最后导入的那一个，项目里其他样本看不到 | `desktop/ui/app.js:473` |
| 跑流程在 Task Center，结果要从任务表的 Output 列点进去才会打开；跑完的 .hyb 不会自动变成项目里的一个样本 | `desktop/ui/app.js:656` |
| 结构预测有两个入口：Task Center 里一个只能粘贴序列的 CPLfold 表单，以及分析页的 RNA Structure 页。保存的结构结果又显示在 Project Home 的“Analysis history”里 | `index.html:128`、`app.js:424` |
| Runtime 是一个主导航项，但对大多数用户来说只是诊断信息 | `index.html:20` |

根本原因是：**桌面版没有自己的数据模型**，只是把文件内容塞给网页版；网页版又是围绕“上传一个文件”设计的。

#### 新的信息架构

只保留**一套导航**，所有页面都读取同一个项目模型（3.1 节：参考 → 样本 → 条件 → 结果）：

```
┌ 顶栏 ─────────────────────────────────────────────────────────────┐
│ ZIKV project ▾ │ 参考: ZAFR ▾ 基因: ZIKV_African… │ 选择: AFR(2) vs SIN(3) │ ⏵ 3 个任务 62% │
├──────────┬────────────────────────────────────────────────────────┤
│ ① 数据   │  样本表：名称 | 条件 | 重复 | 原始文件 | 比对 | .hyb | 索引 | 杂交体数 │
│ ② 质控   │  ZAFR_REP1 | AFR | 1 | 10.2 GB ✓ | ✓ | ✓ | ✓ | 573 k            │
│ ③ 探索   │  ZSIN_REP3 | SIN | 3 | 11.9 GB   | ▓▓▓░ 58% Bowtie2 …           │
│ ④ 比较   │  [导入原始数据] [导入样本表] [运行选中样本]                        │
│ ⑤ 结构   │                                                                  │
│ ⑥ 报告   │                                                                  │
└──────────┴────────────────────────────────────────────────────────┘
```

| 页面 | 作用 | 取代现在的 |
|---|---|---|
| **① 数据** | 参考序列和样本表；导入原始文件或样本表；每个样本有一行阶段状态（比对 → .hyb → 索引），可以选中样本直接运行或续跑 | Project Home 的导入表单、Task Center 的 pipeline 表单 |
| **② 质控** | 每个样本的 reads、比对率、嵌合体比例、杂交体类型；重复之间的相关性（例如 ZSIN_REP3 深度异常这类问题在这里就能看到） | 网页版的 Overview（目前只看一个文件） |
| **③ 探索** | Interactions、Contact map、Viewpoint、Region，作用于顶栏的“当前选择”：一个样本，或一个条件下合并的重复 | 网页版的对应几页 |
| **④ 比较** | 从项目的条件里选 A 和 B（不再选文件），得到差异 bin 和合并后的相互作用；每一行都可以“在 contact map 里定位”或“折叠这个区域” | 网页版的 Compare |
| **⑤ 结构** | 全基因组折叠和局部窗口折叠；证据来自当前选择的样本；结果列表在这里，可以并排对比两个条件的结构 | Task Center 的 CPLfold 表单、网页版的 RNA Structure、Project Home 的 Analysis history |
| **⑥ 报告** | 从项目已有的结果里挑图和表来组装报告 | 网页版的 Report |

不放进主导航的内容：

- **任务**改成顶栏的进度指示和一个可展开的抽屉，不再是一个页面。样本行和结构结果行会显示自己的进度。
- **Runtime** 放进“偏好设置 → 运行环境”，CPU 和内存预算也在这里设。只有缺少组件时才在相关页面上提示。
- **Methods / File format / Validation / Privacy** 放进“帮助”菜单。Privacy 是网页版特有的内容，在桌面版里不显示。

#### 页面之间要串起来的路径

1. 导入原始数据后，运行完的样本自动进入质控和探索，不需要手动“打开结果”。
2. 在 Contact map 上框选一个区域，可以跳到 Region 看细节，也可以“折叠这个区域”，直接以这个区域为窗口进入结构页。
3. 在比较页的一行差异相互作用上点击，可以定位到 contact map，或分别用两个条件的证据各折叠一次，再并排比较（复现 reproduce_zikv 里的 region A/B）。
4. 在结构页看一个预测时，可以叠加显示支持它的嵌合体，并标出它和比较页里哪些差异相互作用重合。
5. 顶栏的“当前选择”在所有页面之间保持一致，切换页面时不会丢失。

#### 实现方式

- 把 `web/app.js` 拆成：**页面组件**（Contact map、Interactions……）+ **数据接口**（`DataProvider`）+ **外壳**（导航、落地页、上传）。
  - 网页版：`BrowserProvider` 在 worker 里解析上传的文件。网页版自己的外壳保留，上传一个文件相当于建一个只有一个样本的临时项目。
  - 桌面版：`DesktopProvider` 调用 3.3 节的 bridge 查询。外壳由 `desktop/ui` 提供，不再嵌入网页版的落地页、导航和路由。
- 页面组件只能通过 `DataProvider` 拿数据，不能直接读文件，也不能用 `FileReader`。这样一套组件可以同时用于两个版本，也避免大文件再进入 WebView。
- 顶栏的“当前选择”（参考、基因、样本或条件）是唯一的全局状态，存进 `ui_state.desktop`，打开项目时恢复。

## 4. 分阶段计划

| 阶段 | 内容 | 可并行的工作线 | 完成标准 |
|---|---|---|---|
| **P0 基线** | 从 ZAFR_REP1 里取 200 万 reads 做回归 golden；写一个记录用时、峰值内存、磁盘的基准脚本；测 10.8 kb 含假结 Phase 2 的用时和内存 | 1 条 | 基准报告提交进仓库 |
| **P1 流式 pipeline** | 3.2 的全部内容：外部排序、Bowtie2 管道、按 read 流式识别杂交体、checkpoint、可选的 SAM 保留 | A：读取和去重；B：比对到 blast 的管道；C：杂交体识别和 collapse 的外部排序 | G1、G2；goldens 逐字节一致 |
| **P2 项目与样本** | 3.1 的全部内容：原地引用、识别 gz、样本表、批量排队、续跑、磁盘预检 | D：Python 端 project；E：Rust 调度和 UI | G3 |
| **P3 分析后端** | 3.3 的全部内容：移植 `hyb2_coverage`、建索引、新 bridge 操作、WebView 切到后端 | F：Python 索引和查询；G：前端适配 | G4；contact.txt 与命令行逐字节一致 |
| **P3.5 界面统一** | 3.7 的全部内容：拆分 `web/app.js`（页面组件、DataProvider、外壳），换成单一导航和顶栏的当前选择，任务改成抽屉，Runtime 和帮助移出主导航，串起页面之间的路径 | K：拆分 web 和 BrowserProvider（网页版行为不变）；L：桌面外壳、数据页、DesktopProvider | 网页版现有的验证脚本全部通过；桌面版只有一套导航；从导入原始数据到折叠一个差异区域，全程不离开项目、不手动选文件 |
| **P4 结构** | 3.4 的全部内容：稀疏 bonus、按内存估算长度、`structure_batch`、弧图查看器 | H：runtime；I：前端 | G5、G6；全基因组 dbn 与 `analysis/cplfold/*.dbn` 一致 |
| **P5 比较** | 3.6（已完成，见 3.6 末尾） | J | G7 |
| **P6 端到端验收** | 在 APP 里跑完 14 个样本、6 组比较和 2 个全基因组折叠，与 reproduce_zikv 逐项对比 | — | 验收报告 |

P1 和 P2 互不依赖，可以同时开始。P3.5 的拆分（K）不依赖后端，可以和 P1 同时做；L 需要 P2 的样本模型和 P3 的查询接口。P3 需要 P1 的输出格式先定下来；P4 的稀疏 bonus 和长度估算可以和 P1 同时做，读取全部 arm 的部分等 P3。

## 5. 风险

- **逐字节一致与流式化的冲突**：legacy 的一些行为依赖全局顺序，例如 `mtophits` 的全局 `best`、collapse 时“第一行被丢”、BWK 的哈希顺序。外部排序必须完全复现排序键；做不到的地方要像 `--reorder` 一样记录成有意差异，并在 golden 里锁定。
- **ranking 需要全局信息**：`remove_duplicate_hybrids` 依赖整个样本的基因排名，所以必须先走一遍算出排名。ZIKV 这种单一参考的情况不受影响，但多基因参考需要专门的测试。
- **含假结的全基因组预测**可能不可行（P0 会量出来）。如果不可行，就把产品定位定为“全基因组不含假结 + 窗口内含假结”。
- **legacy 跑的结果没有用 `--reorder`**，所以 G2 比较 14 个样本时，只要求杂交体数和 contact map 在容差内一致，不要求逐字节一致。
- **磁盘**：即使 SAM 默认不落盘，外部排序的临时文件也约等于 1 倍输入大小。临时目录要能配置，并在任务结束时清理。

## 6. 需要你决定的事（括号里是建议的默认值）

1. 原始数据原地引用还是复制进项目？（原地引用，复制作为可选项）
2. 默认保留 SAM 吗？（不保留，可选 gzip 保留）
3. 差异比较用 PyDESeq2 还是捆绑 R？（PyDESeq2，再用 reproduce_zikv 的结果验证）
4. 桌面版是否完全去掉嵌入的网页版落地页和上传入口，只保留项目里的数据？（是；网页版保留上传）
5. 第一个版本是否先只做 P0 到 P3.5，即先保证“原始数据到 .hyb 到分析”在真实规模下能跑通？（是）

## 7. 实施契约（第一波，按建议默认值）

各工作线在独立 worktree 里并行开发，只能通过下面的接口互相依赖。接口如需改动，必须写进各自的报告。

**R1 流式 pipeline（`pipeline/hyb2pipe`，工作线 A）**
- `pipeline.run(input, reference, out_prefix, options, progress, log)` 的签名不变。`Options` 增加 `keep_sam: "none"|"gz"|"plain"`（默认 `"none"`）、`tmp_dir: Optional[Path]`、`memory_mb: int = 1024`（外部排序的内存预算）、`resume: bool = True`。
- 断点文件 `<out_prefix>.checkpoint.json`：`{"version":1,"input":{path,size,mtime},"reference":{...},"options":{...},"stages":{"<stage>":{"done":true,"outputs":[...]}}}`。如果输入或参数变了，断点作废。
- progress 回调保持 `(stage, detail)`；另外加一个可选的 `progress_bytes(stage, done, total)`。CLI 的 `--json-progress` 在事件里附带 `bytes_done` 和 `bytes_total`。
- CLI 新增参数 `--keep-sam {none,gz,plain}`、`--tmp-dir`、`--memory-mb`、`--no-resume`。
- 输出文件名不变：`.hyb`、`_homodimers.hyb`、`_hybrids.hyb_stats.txt`。

**R2 项目 schema 2.0（`desktop/runtime/project.py` 和 bridge，工作线 B）**
- `schema_version: "2.0"`。打开 1.0 项目时自动迁移，原有的 `inputs` 保留。
- `references[]`：`{id, label, path, mode:"linked"|"copied", size_bytes, mtime, quick_hash, sequences:[{name,length}]}`。
- `samples[]`：`{id, name, condition, replicate, reference_id, gene_id, reads:{path, mode, format, size_bytes, mtime, quick_hash, sha256|null}, settings:{}, outputs:{hyb, homodimers, stats, contact, index}, stages:{map, hyb, index: {status, task_id, updated_at}}}`。format 取值为 `fastq`、`fastq.gz`、`fasta`、`fasta.gz`、`sam`、`hyb`。
- 原地引用的文件存绝对路径，复制进项目的文件存相对路径。`quick_hash` = sha256(文件大小 + 头 1 MiB + 尾 1 MiB)。每个样本的输出放在 `samples/<sample_id>/`。
- bridge 操作：`add_reference`、`add_sample`、`update_sample`、`remove_sample`、`import_sample_sheet{path, sheet}`（TSV 或 CSV；兼容 `ZIKV_dataset_map.tsv` 里用 `;` 分隔多个文件的写法，每个文件拆成一个重复）、`check_inputs`（返回 ok / missing / changed）、`disk_estimate{path, sample_ids}`、`sample_pipeline_run{project_path, sample_id, task_id, run_dir}`（依次执行 hyb2pipe.run → coverage → 建索引，并更新 `stages`）。
- 长任务原子写入 `run_dir/progress.json`：`{stage, detail, bytes_done, bytes_total, updated_at}`。

**R3 分析后端（新包 `pipeline/hyb2analysis`，工作线 C）**
- `coverage.write_contact(hyb, gene_id, out)`：输出与 `bin/hyb2_coverage` 的 contact.txt 逐字节一致。如果需要，forTable 也同样处理。
- `index.build(hyb, index_path)`：生成 SQLite 文件，表 `hybrids` 存每个杂交体的坐标、计数、dG、类型，表 `meta` 存元信息。
- 查询：`summary(indexes)`、`contact_bins(indexes, gene, bin_size, region=None)`、`hybrids(indexes, filters, limit, offset)`、`top_interactions(indexes, gene, bin_size, n)`。`indexes` 是一个列表，传入多个时把计数相加，用于合并重复。
- bridge 操作：`analysis_summary`、`analysis_contact_bins`、`analysis_hybrids`、`analysis_top`。参数都是 `{project_path, sample_ids, ...}`，样本对应的索引路径从项目里读取。

**R4 前端拆分（`web/`，工作线 D）**
- 新增 `DataProvider` 接口：`listDatasets`、`getSummary`、`getContactBins`、`queryHybrids`、`getTopInteractions`、`getSequence`、`getArms`，全部是异步方法，参数里带 `selection`。
- `BrowserProvider` 用现有的 worker 和内存数据实现这个接口。网页版的行为和 `scripts/verify-*.cjs` 的结果保持不变。
- 新增 `window.Hyb2Web.mount(host, {provider, shell})`：`shell:false` 时不渲染落地页、侧栏、上传入口和帮助页，页面由宿主通过 `navigate(pageId)` 切换。

**R5 结构（`web/cplfold`、`desktop/runtime`，工作线 E）**
- 稀疏 bonus 构建：用 Numba 只在 ±5.3σ 以内累加，和稠密版的最大误差 ≤ 1e-4。
- 用 `cplfold_limits()` 替换 `cplfold_max_nt()`：根据可用内存估算允许长度，`HYB2_CPLFOLD_MAX_NT` 只作为调试时的覆盖值。
- bridge 操作 `structure_run{project_path, sample_ids, gene_id, mode:"genome"|"windows", windows:[{start,end}], parameters, workers, task_id, run_dir}`：结果写进 `structures/<task_id>/`（dbn、pairs.tsv、ct、meta.json），windows 模式用进程池并行。
- P0 测量：10.8 kb 含假结时 Phase 2 的用时和内存。

**R6 Rust 调度（`desktop/src-tauri`，工作线 F）**
- 新增任务类型 `sample_pipeline{sample_id}`、`analysis_index{sample_id}`、`structure{...R5}`，映射到 R2 / R5 的 bridge 操作。
- 资源预算：每个任务声明需要多少 `cpu` 和 `mem_mb`，超出预算的任务进入 `queued` 状态，等资源释放后自动启动。预算保存在应用配置目录的 `preferences.json` 里，默认是全部核数和 75% 内存。
- 任务快照里带上 `progress.json` 的内容。
- `task_resume{task_id}`：用同一份请求重新启动一个 `interrupted` 或 `failed` 的任务，断点由 Python 端负责。
- 新增 Tauri 命令 `analysis_query{operation, ...}`，在 compute lane 上转发到 R3 的四个查询操作。
