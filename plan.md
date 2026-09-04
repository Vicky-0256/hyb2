# HYB2 Web Lite 页面设计

## 一、产品定位

**HYB2 Web Lite** 是一个完全运行在浏览器中的 RNA–RNA interaction 分析工具。

用户流程：

```text
打开网页
  ↓
选择自己的 .hyb 文件
  ↓
浏览器本地解析与验证
  ↓
查看 RNA interaction、contact map 和区域数据
  ↓
可选加载 reference FASTA
  ↓
进行 RNA 二级结构预测
  ↓
下载图表、筛选结果和结构文件
```

核心产品承诺：

> **用户文件只在本地浏览器中处理，不上传、不保存、不发送到服务器。**

GitHub Pages 仅提供 HTML、JavaScript、CSS 和 WebAssembly 程序文件。

---

# 二、网站整体信息架构

网站建议采用单页应用，主要页面如下：

```text
HYB2 Web
│
├── 首页 / 文件加载
│
├── 分析工作台
│   ├── Overview
│   ├── Interactions
│   ├── Contact Map
│   ├── Viewpoint
│   ├── Region Explorer
│   ├── Compare
│   └── RNA Structure
│
├── Methods
├── File Format
├── Privacy
└── About
```

在 GitHub Pages 上建议使用 Hash Router：

```text
/#/
/#/workspace/overview
/#/workspace/interactions
/#/workspace/contact-map
/#/workspace/viewpoint
/#/workspace/region
/#/workspace/compare
/#/workspace/structure
/#/methods
```

这样刷新页面时不会因为 GitHub Pages 缺少服务器路由而出现 404。

---

# 三、全局页面布局

桌面端使用左侧导航栏、顶部工具栏和中央内容区。

```text
┌──────────────────────────────────────────────────────────────────────┐
│ HYB2 Web     sample.hyb · Ready       Local processing   Files  Help │
├──────────────────┬───────────────────────────────────────────────────┤
│                  │                                                   │
│ DATA             │                                                   │
│ sample.hyb       │                Main content                       │
│ 12.4 MB          │                                                   │
│ 83,291 records   │                                                   │
│                  │                                                   │
│ ANALYSIS         │                                                   │
│ ● Overview       │                                                   │
│   Interactions   │                                                   │
│   Contact Map    │                                                   │
│   Viewpoint      │                                                   │
│   Region Explorer│                                                   │
│   Compare        │                                                   │
│   RNA Structure  │                                                   │
│                  │                                                   │
│ DATA MANAGEMENT  │                                                   │
│   Files          │                                                   │
│   Validation     │                                                   │
│   Clear session  │                                                   │
│                  │                                                   │
│ Files stay local │                                                   │
└──────────────────┴───────────────────────────────────────────────────┘
```

建议尺寸：

| 区域      |        桌面端 |
| ------- | ---------: |
| 顶部栏     |      64 px |
| 左侧导航    | 240–256 px |
| 主内容最大宽度 |   1,440 px |
| 页面内边距   |   24–32 px |
| 卡片间距    |   16–24 px |
| 右侧详情抽屉  | 360–420 px |

---

# 四、顶部工具栏

顶部栏始终固定。

## 左侧

```text
HYB2 Web
sample.hyb
Ready
```

文件名过长时截断：

```text
COMRADES_Zika_replica...
```

鼠标悬停显示完整文件名。

## 右侧

```text
[ Local processing ] [ Files ] [ 中文/EN ] [ Help ] [ Theme ]
```

### Local processing 状态

使用带盾牌图标的状态标签：

```text
🛡 Local processing
```

点击打开隐私说明：

```text
Your data stays on this device

• HYB and FASTA files are read locally
• Files are not uploaded
• Results are not stored on a server
• Closing the page clears the session unless local persistence is enabled
```

建议所有 JavaScript、WebAssembly、图表库和字体都打包到网站中，不通过第三方 CDN 加载。

---

# 五、首页：文件加载页面

## 页面目标

让用户在十秒内理解：

1. 这是做什么的；
2. 上传什么文件；
3. 文件不会离开电脑；
4. 不需要账号。

## 页面线框

```text
┌────────────────────────────────────────────────────────────┐
│                                                            │
│                       HYB2 Web                             │
│                                                            │
│         Explore RNA–RNA interactions in your browser       │
│                                                            │
│   Analyse contact maps, interaction partners and RNA       │
│   secondary structures from your own HYB files.            │
│                                                            │
│   ┌────────────────────────────────────────────────────┐   │
│   │                                                    │   │
│   │               Drop a .hyb file here                │   │
│   │                                                    │   │
│   │                    or                              │   │
│   │                                                    │   │
│   │                [ Choose HYB file ]                 │   │
│   │                                                    │   │
│   │        Recommended file size: up to 50 MB          │   │
│   │                                                    │   │
│   └────────────────────────────────────────────────────┘   │
│                                                            │
│              [ Try with an example dataset ]                │
│                                                            │
│   ✓ No account       ✓ No server upload       ✓ Free       │
│                                                            │
└────────────────────────────────────────────────────────────┘
```

## 文件选择区域

支持：

* 点击选择；
* 拖放；
* 键盘操作；
* `.hyb`；
* 可兼容 `.txt`，但需要通过内容验证；
* 一次只分析一个主要 `.hyb`。

文件输入：

```html
<input type="file" accept=".hyb,.txt,text/plain">
```

界面文字建议使用：

```text
Choose HYB file
```

而不是只写：

```text
Upload
```

因为文件实际不会传到服务器。

可以保留用户熟悉的描述：

```text
Upload or open your HYB file
Processed locally in your browser
```

---

# 六、文件加载和解析过程

用户选择文件后，不应该立即跳进一个空白页面。需要明确展示处理阶段。

```text
Preparing sample.hyb

✓ Reading file
✓ Detecting format
● Parsing HYB records
○ Building RNA index
○ Calculating summary
```

进度信息：

```text
Parsed 52,000 of approximately 83,000 rows
63%
```

对于大于 20 MB 的文件，建议使用：

```text
File.stream()
+
TextDecoder
+
Web Worker
```

避免使用一次性：

```javascript
await file.text()
```

因为后者会同时在内存中保留完整文本和解析对象。

## Web Worker 通信

```text
Main thread
   │
   │ File
   ▼
HYB Web Worker
   │
   ├── validate
   ├── parse
   ├── build indices
   ├── calculate statistics
   └── build contact matrices
   │
   ▼
React UI
```

---

# 七、文件格式验证页面

当前 `.hyb` 记录由两个 RNA arms 组成，核心字段包括 RNA 名称、read 坐标、RNA 坐标、e-value、sequence、dG、overlap score 和 chimera type。

建议解析以下列：

| 索引 | 字段                  |
| -: | ------------------- |
|  0 | Sequence/read ID    |
|  1 | Hybrid sequence     |
|  2 | dG                  |
|  3 | Arm 1 RNA           |
|  4 | Arm 1 read start    |
|  5 | Arm 1 read end      |
|  6 | Arm 1 RNA start     |
|  7 | Arm 1 RNA end       |
|  8 | Arm 1 e-value       |
|  9 | Arm 2 RNA           |
| 10 | Arm 2 read start    |
| 11 | Arm 2 read end      |
| 12 | Arm 2 RNA start     |
| 13 | Arm 2 RNA end       |
| 14 | Arm 2 e-value       |
| 15 | Overlap score（标准 17 列格式的第 16 列） |
| 16 | Chimera type（标准 17 列格式的第 17 列）  |

计数语义必须与现有 HYB2 脚本一致：经 `collapse_hyb_2.sh` 聚合后的 source/raw-read count 位于 sequence ID 按下划线分割后的第二个字段，而不是第 16 列。标准第 16 列是 overlap score，第 17 列是 `Type_1`–`Type_13` 或 `Intermolecular`。旧的 clustered 16 列文件可以从 `count_total=…` metadata 读取 cluster support；只有整数第 16 列而没有第 17 列时必须标为歧义旧格式。

Homodimer 也不是 chimera type。与 `sam2hyb` 输出规则一致，Web 版仅在两个 arms 指向同一 RNA 且 overlap score ≥ 5 时标记 homodimer proxy。

## 成功状态

```text
sample.hyb is ready

Valid records             83,291
Skipped comment lines         14
Skipped blank lines             2
Invalid records                 7

[ Continue with 83,291 valid records ]
[ View validation report ]
```

## 警告状态

允许少量错误记录被跳过，但必须告诉用户。

```text
File loaded with warnings

7 records could not be parsed.

Reasons:
• 4 rows contain fewer than 15 columns
• 2 rows contain invalid coordinates
• 1 row contains a missing RNA name
```

提供：

```text
[ Download validation report ]
```

报告格式：

```csv
line,error,content
1204,"Expected at least 15 columns","..."
3819,"Invalid arm2 start","..."
```

## 阻断性错误

```text
This does not appear to be a valid HYB file

No records with at least 15 tab-separated columns were found.

[ Choose another file ]
[ View HYB file format ]
```

---

# 八、加载完成后的参考 FASTA 提示

`.hyb` 成功加载后进入 Overview，同时在页面顶部显示一个非阻断提示：

```text
Reference sequences not loaded

Interaction analysis and contact maps are available now.
Add a FASTA file to enable reference-region folding and full-length axes.

[ Add reference FASTA ] [ Dismiss ]
```

参考 FASTA 是可选文件：

* 无 FASTA：可做 summary、interaction、contact map、region filtering；
* 有 FASTA：可提取完整 RNA 区域并做二级结构预测；
* `.hyb` 第二列中的 sequence 可以显示，但不应默认当作完整参考 RNA 区域。

Viewpoint 可以使用用户选择的坐标范围；加载并匹配参考 FASTA 后，还可以切换到完整 mapped-reference 范围（1 到参考序列长度）。没有 FASTA 时只能使用 `.hyb` 中观察到的坐标范围或用户输入的有效范围。

---

# 九、Overview 页面

## 页面目标

让用户快速了解文件中：

* 有多少有效记录；
* 有多少 RNA；
* 哪些 RNA interaction 最多；
* 分子内与分子间 interaction 比例；
* 数据质量如何。

## 页面布局

```text
Overview                                      [ Export summary ]

sample.hyb
Loaded locally · 83,291 valid records

┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ HYB records  │ │ Source reads │ │ Unique RNAs  │ │ RNA pairs    │
│ 83,291       │ │ 102,483      │ │ 47           │ │ 218          │
│              │ │ from IDs     │ │              │ │              │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘

┌────────────────────────────────┐ ┌──────────────────────────────┐
│ Top interacting RNAs           │ │ Interaction types            │
│                                │ │                              │
│ U6    █████████████  24,381    │ │ Intra-RNA       38%          │
│ U2    ██████████     18,209    │ │ Inter-RNA       62%          │
│ 28S   ███████        12,902    │ │                              │
│ U4    █████           8,702    │ │ [ View interactions ]        │
└────────────────────────────────┘ └──────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ Top RNA pairs                                                  │
│                                                                │
│ RNA 1        RNA 2        Records       Cluster support         │
│ U6           U2           12,381        12,381                  │
│ U6           U4            8,934         8,934                  │
│ 28S          5.8S          4,128         4,128                  │
└────────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────────┐
│ Data quality                                                   │
│ 99.99% valid rows · 12% include dG · 100% include coordinates  │
└────────────────────────────────────────────────────────────────┘
```

## 必须区分三个数量

### Records

每一行算一条：

```text
83,291 HYB records
```

### Source reads

对于 `collapse_hyb_2.sh` 生成的 ID，例如：

```text
read_12
```

下划线分割后的第二个字段 `12` 是该 collapsed record 代表的 source/raw-read count。它作为独立的 provenance 和 summary 指标显示，不改变 Contact Map 或 Viewpoint 的逐行累加规则。

### Cluster support

旧的 clustered 文件可以包含：

```text
count_total=...
```

该值只作为 legacy cluster support 使用。标准 17 列文件的第 16 列 overlap score 绝不能被当成 count。

界面 tooltip：

```text
Source reads are recovered from the second underscore-delimited ID field.
Standard column 16 is an overlap score, not a count.
Contact Map and Viewpoint count each row once unless legacy cluster support is explicitly selected.
```

避免把“记录数”、“source reads”和旧格式的“cluster support”混为一谈。

---

# 十、Interactions 页面

## 页面目标

提供 RNA partner 分析、数据筛选和记录浏览。

## 顶部筛选栏

```text
Interactions

RNA
[ U6 ▼ ]

Partner
[ All partners ▼ ]

Interaction type
[ All ▼ ]

Homodimer subset
[ All records ▼ ]

Coordinates
[ Any region ▼ ]

Count mode
[ Record count ▼ ]

Search
[ RNA, sequence ID or sequence... ]
```

高级筛选折叠区：

```text
Advanced filters
────────────────────────────────

Arm 1 coordinate       [       ] – [       ]
Arm 2 coordinate       [       ] – [       ]
Maximum e-value        [ 0.1               ]
dG range               [       ] – [       ]
Minimum cluster support [ 1                ]
Orientation            [ Normalised pairs ▼ ]
```

## Partner 图

```text
U6 interaction partners

U2      ███████████████████████    12,381
U4      ███████████████             8,934
28S     ███████                     4,128
5.8S    █████                       2,983
U6      ████                        2,102
```

点击柱形后：

* 自动将 Partner 设置为该 RNA；
* 更新下方表格；
* 更新 URL 状态；
* 提供“打开 contact map”。

## 记录表格

```text
83,291 records · Showing 1–100

┌────────────┬────────┬────────────┬────────┬────────────┬───────┬──────┐
│ Sequence ID│ RNA 1  │ Region 1   │ RNA 2  │ Region 2   │Support│ dG   │
├────────────┼────────┼────────────┼────────┼────────────┼───────┼──────┤
│ read_10392 │ U6     │ 102–119    │ U2     │ 391–409    │ 1     │ -8.2 │
│ read_10431 │ U6     │ 121–143    │ U2     │ 412–433    │ 3     │ —    │
└────────────┴────────┴────────────┴────────┴────────────┴───────┴──────┘
```

表格必须使用虚拟滚动，不能把几十万行全部渲染到 DOM。

推荐：

```text
TanStack Table
+
TanStack Virtual
```

## 记录详情抽屉

点击一行，从右侧打开：

```text
Interaction record

Sequence ID
read_10392

Sequence
AGTCAGCGATCG...

Arm 1
RNA             U6
RNA coordinates 102–119
Read coordinates 1–18
E-value          0.003

Arm 2
RNA             U2
RNA coordinates 391–409
Read coordinates 22–40
E-value          0.007

dG               -8.20
Cluster support   1
Source reads      12
Overlap score     6
Homodimer proxy   Yes
Chimera type      Type_3

[ Open in Contact Map ]
[ Explore RNA 1 region ]
[ Explore RNA 2 region ]
[ Fold sequence ]
[ Copy record ]
```

“Fold sequence”必须注明：

```text
Fold this HYB record sequence
```

不要把它描述成完整 RNA 区域预测。

---

# 十一、Contact Map 页面

## 核心分析规则

当前 bundled AWK `plot_hybrids_3.awk` 在 entire-hybrid 模式下使用固定 bin，把 arm 1 和 arm 2 覆盖到的每一对 bin 累加；网页默认采用 10 nt bin。

Web 版应该保持相同默认逻辑：

```text
arm 1 start/end → X bins
arm 2 start/end → Y bins
每一个 X/Y bin pair + 1（或用户明确选择的 legacy cluster support）
```

当前 coverage 层是先生成 contact 数据，再调用 Rscript 绘图；浏览器版可以保留相同 contact 计算，用交互式 Web heatmap 替代 R 图。

## 页面布局

```text
Contact Map

┌───────────────────────────────────────────────────────────────┐
│ RNA X       [ U6 ▼ ]      RNA Y       [ U2 ▼ ]               │
│                                                               │
│ Bin size    [ 10 nt ▼ ]   Measure     [ Record count ▼ ]      │
│ Colour cap  [ 95% ▼ ]     Scale       [ Linear ▼ ]            │
│                                                               │
│ [ Generate ]                           [ Reset zoom ]           │
└───────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────┬──────────────────┐
│                                            │ Selected region  │
│                                            │                  │
│                Heatmap                     │ X: 100–300       │
│                                            │ Y: 380–520       │
│                                            │                  │
│                                            │ Records: 1,283   │
│                                            │ Weight: 1,492    │
│                                            │                  │
│                                            │ [Explore region] │
│                                            │ [Export records] │
└────────────────────────────────────────────┴──────────────────┘

[ Download SVG ] [ Download PNG ] [ Export contact TSV ]
```

## 控件

### RNA X / RNA Y

* 从 `.hyb` 自动生成可搜索下拉框；
* 默认选择 interaction 数最多的 RNA pair；
* 同一个 RNA 允许自交互。

### Bin size

建议提供：

```text
5 nt
10 nt     default
25 nt
50 nt
100 nt
```

文件较大或 RNA 很长时，动态提醒：

```text
A 5 nt bin size may create a very large matrix.
```

### Measure

```text
Record count
Legacy cluster support
```

### Colour cap

与现有 HYB2 默认参数保持一致：

```text
95th percentile
```

同时提供：

```text
90%
95%
99%
Maximum
Custom
```

### Scale

```text
Linear
Log1p
```

Log1p 只改变显示，不改变原始输出。

## Heatmap 交互

鼠标悬停：

```text
RNA X: U6
Position: 120–129

RNA Y: U2
Position: 390–399

Records: 27
Cluster support: 31
```

支持：

* 滚轮缩放；
* 拖动；
* 双击重置；
* 框选区域；
* 点击单元格查看对应记录；
* 键盘导航；
* 色盲友好的 Viridis 色阶。

## RNA arm 方向处理

对于两个不同 RNA，默认将用户选择的 RNA X 规范到 X 轴，即：

```text
U6–U2
U2–U6
```

统一显示为：

```text
X = U6
Y = U2
```

界面中提供说明：

```text
Arm orientation normalised
```

并允许切换为：

```text
Original arm orientation
```

## Viewpoint navigation

Viewpoint 对选定 RNA 的两个 interaction arms 进行逐行 coverage 累加，并提供 homodimer proxy 的 include/exclude/only 筛选。坐标轴支持两种明确模式：

```text
Selected coordinates
Full mapped reference
```

`Full mapped reference` 只有在该 HYB RNA 已匹配到 FASTA 时可用，其范围固定为 1 到参考序列长度；否则页面使用观察到的 HYB 坐标或用户选择的坐标。选定的 Viewpoint 区间可以继续传入 Region Explorer。

---

# 十二、Region Explorer 页面

## 页面目标

从某个 RNA 区域出发，查看所有与该区域重叠的 interactions。

## 页面布局

```text
Region Explorer

Anchor RNA
[ U6 ▼ ]

Region
[ 100 ] ───────────────────── [ 300 ]

Partner
[ All partners ▼ ]

Overlap rule
[ Any overlap ▼ ]

[ Explore ]
```

## 结果区域

```text
U6:100–300

1,283 matching records
1,492 legacy cluster support
14 partner RNAs

┌──────────────────────────────────────────────────────────────┐
│ Position profile                                             │
│                                                              │
│ 100         150         200         250         300           │
│ ─────██████████████──────████████────────████████──          │
└──────────────────────────────────────────────────────────────┘

Top partners

U2      █████████████████    482
U4      ███████████          319
28S     █████                141
```

## 有 FASTA 时

显示区域 sequence：

```text
Reference sequence

>U6:100-300
AUGCUUGCAG...
```

提供：

```text
[ Copy sequence ]
[ Send to RNA Structure ]
[ Download FASTA ]
```

## 无 FASTA 时

显示：

```text
Reference sequence unavailable

Add a reference FASTA file to extract the selected RNA region.

[ Add reference FASTA ]
```

## 从 Contact Map 进入

用户在 Contact Map 框选区域后点击：

```text
Explore region
```

Region Explorer 自动填充：

```text
RNA X
X start/end
RNA Y
Y start/end
```

---

# 十三、RNA Structure 页面

## 页面定位

当前结构预测提供两个独立引擎：

> **浏览器中的 ViennaRNA WebAssembly folding 与 CPLfold pure-Python/Pyodide pseudoknot 引擎。**

浏览器当前提供 plain global MFE、手动 hard pairs，以及 HYB-guided RNAcofold evidence 三种模式。第三种模式实现 `.hyb` 之后的 ViennaRNA 路径：每条满足区域条件的 HYB row 独立 RNAcofold、base-pair-frequency 聚合、touching stem 合并与排序、逐条兼容性约束拟合、可复现的随机约束顺序、COMRADES support scoring 和 evidence colouring。UNAFold 保留为可选外部 CLI 兼容路径。

CPLfold 是独立 engine，不复用 ViennaRNA 的 `constraintMode` 语义。它可做 sequence-only 预测，也可把单个 mapped reference region 内每条 eligible HYB row 的两个区间按 CPLfold/IRIS 方法转换为 Gaussian arm support、symmetric outer product、1e-6 threshold 和 `log1p` bonus matrix，再运行 two-phase pseudoknot search。结果显示完整 bonus matrix、nested/pseudoknot candidates、phase-1 与 crossing phase-2 arc layers，并提供 DBN、CT、SVG、PNG、base-pair、bonus、candidate 和 provenance exports。

页面根据 folding mode 显示方法标签：

```text
Plain MFE: ViennaRNA MFE · Browser
Manual constraints: ViennaRNA hard pairs · Browser
HYB-guided: RNAcofold + constrained RNAfold · Browser
CPLfold: CPLfold + Pyodide · Browser
```

以及限制：

```text
HYB-guided mode reproduces HYB2's post-HYB ViennaRNA evidence and constraint
workflow. General pseudoknots remain outside ViennaRNA dot-bracket output.
CPLfold supports pseudoknot dot-bracket layers, but the pure-Python Pyodide
runtime has no Numba JIT and is therefore capped at 75 nt. Longer runs use the
local bin/cplfold command.
```

## 页面整体布局

```text
RNA Structure

┌──────────────────────────────┬─────────────────────────────────────────┐
│ Folding setup                │ Structure result                        │
│                              │                                         │
│ Sequence source              │                                         │
│ [ Reference region ▼ ]       │          RNA structure viewer           │
│                              │                                         │
│ RNA                          │                                         │
│ [ U6 ▼ ]                    │                                         │
│                              │                                         │
│ Start     End                │                                         │
│ [ 100 ]   [ 300 ]            │                                         │
│                              │                                         │
│ Sequence length: 201 nt      │                                         │
│                              │                                         │
│ Folding mode                 │                                         │
│ ● Plain MFE (default)        │                                         │
│ ○ Manual hard base pairs     │                                         │
│   (expert)                   │                                         │
│                              │                                         │
│ Temperature                  │                                         │
│ [ 37 °C ]                    │                                         │
│                              │                                         │
│ [ Predict structure ]        │                                         │
└──────────────────────────────┴─────────────────────────────────────────┘
```

---

## 13.1 Sequence source

提供三个来源。

### Reference region

推荐模式：

```text
Reference region
```

要求已加载 FASTA：

```text
RNA: U6
Start: 100
End: 300
```

### HYB record sequence

来自某一行 `.hyb` 的第二列：

```text
HYB record sequence
```

页面显示警告：

```text
This is the hybrid/read sequence stored in the HYB record,
not necessarily the full reference RNA region.
```

### Paste sequence

允许直接粘贴：

```text
Paste RNA sequence
```

支持：

* A、C、G、U；
* 自动把 T 转换为 U；
* 自动去除空格和 FASTA header；
* 非法字符报错。

---

## 13.2 Folding mode

### Plain MFE

默认模式：

```text
Plain minimum-free-energy structure
```

输出：

* sequence；
* dot-bracket；
* MFE；
* base-pair list；
* SVG structure。

### Manual hard base pairs（expert）

当前页面提供可选的手动 hard-pair 模式。每行输入一个相对于 prepared sequence 的 1-based `i-j` pair，例如：

```text
4-18
7-15
```

页面在启动 Worker 前验证坐标范围、minimum loop、A–U/G–C/G–U 配对、端点不复用和非交叉约束。ViennaRNA WebAssembly 强制执行这些 pairs，结果页将 hard-pair arcs 加粗并在 base-pair list 和 TSV 中明确标注。Report JSON 使用 `constraintSource: "manual-user-input"`，同时把 automatic HYB/RNAcofold evidence generation 标为 false。默认仍为 Plain MFE；只有用户主动选择 expert mode 时才提交约束。

这些 pairs 完全来自手动输入；只有切换到 HYB-guided 模式时才会从 HYB records 和 RNAcofold evidence 自动推断约束。

### HYB-guided RNAcofold evidence（已实现）

该模式要求已加载 `.hyb` 和匹配的 reference FASTA。Single-region 用于短程 intramolecular；paired-region 用原 HYB2 的 100 nt spacer（50 A + 50 T，RNA 中规范化为 50 A + 50 U）拼接两段区域，用于 long-range、intermolecular 和 homodimer。

```text
Each eligible HYB row → RNAcofold
                       → base-pair frequency
                       → touching stems
                       → top 75 F constraints
                       → ranked + seeded random orders
                       → compatible constrained RNAfold
                       → COMRADES score and evidence colour
```

计数语义严格分离：每条 eligible HYB row 对 evidence 贡献一次；column 16 overlap score、legacy cluster support 和 collapsed raw-read provenance 均不会隐式放大 evidence。保留原脚本 `printed<=1000` 实际最多输出 1,001 行的边界行为。

设置区：

```text
Reference layout           [ Single / Paired ]
Maximum stem constraints   [ 75 ]
Randomised folds           [ 0 / 10 / 100 / 1000 ]
Reproducibility seed       [ HYB2-Web ]
Homodimer overlap ≥5 only  [ ]
```

Plain MFE 仍是默认值；用户必须主动选择 HYB-guided。超过 100 个随机 folds 需要显式确认，最多 1,000；Cancel 通过终止专用 Worker 生效。结果导出 evidence、accepted constraints、全部 ensemble summaries、结构及可复现参数。

---

## 13.3 Folding 长度限制

ViennaRNA 动态规划的浏览器资源消耗会随序列长度显著增加。产品上建议：

|             长度 | 页面行为            |
| -------------: | --------------- |
|        ≤500 nt | 正常              |
|   501–1,000 nt | 正常，但提示可能较慢      |
| 1,001–2,000 nt | 显示明显警告          |
|      >2,000 nt | 默认阻止，允许高级用户手动继续 |
|      >3,000 nt | Web Lite 不支持    |

提示：

```text
This region contains 1,642 nt.

Browser folding may consume substantial memory and temporarily
make this tab less responsive.

[ Reduce region ] [ Continue ]
```

默认 Region Explorer 向 Structure 发送的区域建议控制在 200–500 nt。

---

## 13.4 计算过程

```text
Predicting RNA structure

✓ Loading folding engine
✓ Preparing sequence
● Calculating minimum free-energy structure
○ Rendering structure
```

ViennaRNA WebAssembly 应在 Web Worker 中运行，避免阻塞主线程。

---

## 13.5 结构结果页面

```text
RNA Structure

U6 · 100–300 · 201 nt
ViennaRNA MFE · 37 °C

Constraint mode
None · or Manual hard (N enforced pairs)

MFE
-32.70 kcal/mol

┌───────────────────────────────────────────────────────────────┐
│                                                               │
│                 Interactive RNA structure                     │
│                                                               │
│                       A                                       │
│                    G     U                                    │
│                  C           G                                │
│                  |           |                                │
│                  G           C                                │
│                    U       A                                  │
│                                                               │
└───────────────────────────────────────────────────────────────┘

[ Diagram ] [ Dot-bracket ] [ Base pairs ] [ Sequence ]
```

### Diagram 标签

交互：

* 缩放；
* 平移；
* 点击 nucleotide；
* 显示 nucleotide 编号；
* 显示配对对象；
* 显示 ViennaRNA MFE 配对类型。

点击 nucleotide：

```text
Nucleotide 142
Base: G
Paired with: C188
Pair type: G–C
```

### Dot-bracket 标签

```text
Sequence
GCGCUUCGCCGAG...

Structure
(((....)))....(((((...)))))

MFE
-32.70 kcal/mol
```

使用等宽字体，并提供：

```text
[ Copy ]
```

### Base pairs 标签

| Base 1 | Base 2 | Pair | Source     |
| -----: | -----: | ---- | ---------- |
|    104 |    286 | G–C  | Hard pair  |
|    105 |    285 | C–G  | MFE result |
|    112 |    130 | A–U  | MFE result |

在 manual 模式下，hard-pair arcs 使用更粗的线，nucleotide inspector、base-pair list 和下载文件都应保留该来源标签。

### 导出

```text
[ Download SVG ]
[ Download dot-bracket ]
[ Download CT ]
[ Download base pairs ]
[ Download report JSON ]
```

---

## 13.6 Multi-file Compare（独立页面）

Compare 可以同时加载多个本地 `.hyb` 文件，将数据集分配给 Condition A 或 Condition B，并显示 dataset-level contact counts、每个 condition 的均值、可选的每百万 records 归一化，以及：

```text
log2((A + pseudocount) / (B + pseudocount))
```

该结果是 descriptive effect map，同时可以显示跨数据集 conserved bins。页面导出与现有 `bin/DESeq_run.R` 兼容的 `hyb2-web.table.txt` 和两列、无表头的 `hyb2-web_names.table`；带表头的扩展 sample metadata 另行导出，供审计或后续 WebR/native-R adapter 使用。任何不是非负整数、超过 JavaScript safe integer、或超过旧 R pipeline 的 32 位整数上限的 count 都会被拒绝，而不是静默取整或转成 0。

2026-09-04 的手工浏览器 smoke 使用官方 WebR 0.6.0 release asset，在普通本地静态 origin 通过 PostMessage 启动 base R 4.6.0 并完成数值计算；harness、asset SHA-256 和结果已记录。但当前网站不携带 WebR runtime，deployment CI 也未重跑该 browser smoke，因此它只是技术可行性证据而不是 production release check。稳定 WASM repository 不含 DESeq2。另一个针对 R-universe **development** DESeq2 1.53.2 的探索性探针在 43 个 runtime 包中解析到 42 个，缺少 `locfit`；该结果不能当作 pinned DESeq2 1.52.0 / Bioconductor 3.23 closure 已验证。当前页面不生成 p-value、adjusted p-value 或 statistically significant clusters。先由本地 `DESeq_run.R` 使用兼容导出文件；待 1.52.0 固定依赖镜像、静态加载、deployment browser smoke 和 native-R numerical parity 全部通过后，即可纯静态启用，无需后端。

所有 comparison 文件仍只在浏览器 tab memory 中处理。

---

# 十四、Reference FASTA 管理

FASTA 上传后，需要验证 RNA 名称与 `.hyb` 中名称的对应关系。

## Files 抽屉

```text
Files

Interaction data
✓ sample.hyb
  12.4 MB
  83,291 valid records
  [ Replace ] [ Remove ]

Reference sequences
✓ reference.fa
  47 sequences
  42 matched to HYB RNA names
  [ Review mapping ]
  [ Replace ] [ Remove ]

Privacy
Files are stored in memory only.

[ Clear all local data ]
```

## RNA 名称匹配

建议使用以下顺序：

1. 完整 header 精确匹配；
2. FASTA header 第一个空格前 token 精确匹配；
3. 用户手动匹配。

不要默认进行不可解释的模糊匹配。

映射页面：

```text
Reference mapping

42 of 47 HYB RNA names matched

HYB RNA            FASTA sequence
U6                 U6                     ✓
U2                 U2                     ✓
Zika_virusRNA      Zika_virusRNA          ✓
18S_rRNA           Not matched            [ Select sequence ▼ ]
```

提供：

```text
[ Export mapping JSON ]
```

用于重复分析。

---

# 十五、文件和会话管理

## 默认行为

默认所有数据仅存在于内存：

```text
关闭页面
→ 文件和结果清除
```

## 可选本地保存

可以增加：

```text
Keep this analysis in this browser
```

启用后保存到 IndexedDB。

必须默认关闭，并明确说明：

```text
This stores your files only in this browser on this device.
Nothing is uploaded.
```

## Clear session

左侧底部始终提供：

```text
Clear session
```

点击确认：

```text
Clear this analysis?

This removes the HYB file, reference sequences,
filters and generated results from this browser.

[ Cancel ] [ Clear analysis ]
```

---

# 十六、导出设计

所有主要模块都应允许导出。

## Overview

```text
summary.json
rna_counts.csv
rna_pairs.csv
```

## Interactions

```text
filtered.hyb
filtered_interactions.csv
interactions-parameters.json
```

## Contact Map

```text
contact_map.tsv
contact_map.svg
contact_map.png
contact_map_parameters.json
```

## Viewpoint

```text
viewpoint.tsv
viewpoint.svg
viewpoint.png
viewpoint-parameters.json
```

## Compare

```text
comparison.tsv
comparison.svg
comparison.png
comparison-selected-datasets.tsv
comparison-parameters.json
```

## Region Explorer

```text
region_interactions.hyb
region_interactions.csv
region.fasta
region-parameters.json
```

## Structure

```text
structure.svg
structure.png
structure.dbn
structure.ct
structure.json
```

## Reproducibility 信息

每类派生分析都提供配套的参数或 report JSON；CSV、TSV、SVG、PNG 等便携格式本身不要求内嵌 metadata：

```json
{
  "application": "HYB2 Web",
  "version": "0.5.0",
  "sourceFile": {
    "name": "sample.hyb",
    "sha256": "..."
  },
  "analysis": "contact-map",
  "parameters": {
    "rnaX": "U6",
    "rnaY": "U2",
    "binSize": 10,
    "measure": "records",
    "colourCapQuantile": 0.95
  }
}
```

顶部 Files 抽屉显示：

```text
App version
Git commit
HYB SHA-256
FASTA SHA-256
```

这对科研可重复性非常重要。

---

# 十七、Methods 页面

Methods 页面不应只是营销说明，要明确记录每个分析规则。

建议章节：

```text
1. HYB file format
2. Records, source-read counts and legacy cluster support
3. RNA pair normalisation
4. Contact map binning
5. Region overlap rules
6. Reference FASTA matching
7. RNA structure prediction
8. Original HYB evidence-derived constraints and browser boundary
9. Known limitations
10. Reproducibility
```

每个页面标题旁都有：

```text
ⓘ Method
```

点击直接打开对应方法说明。

Contact Map 方法说明示例：

```text
Coordinates are grouped into fixed-width bins.
For each HYB record, all bins covered by arm 1 are paired
with all bins covered by arm 2. Each pair receives the selected
record weight.
```

这与现有 bundled AWK `plot_hybrids_3.awk` 的嵌套 bin 累加逻辑保持一致。

---

# 十八、视觉设计系统

## 整体风格

建议采用：

> 专业科研工具 + 轻量数据工作台

避免：

* 大面积营销渐变；
* 过度动画；
* 游戏化样式；
* 将科学图表装饰成信息图。

## 色彩

建议基础色：

| 用途            | 色值        |
| ------------- | --------- |
| 页面背景          | `#F6F8FB` |
| 卡片背景          | `#FFFFFF` |
| 主文字           | `#172033` |
| 次文字           | `#5F6B7A` |
| 边框            | `#DDE3EC` |
| Primary       | `#176B87` |
| Primary hover | `#12556C` |
| Accent        | `#6D5BD0` |
| Success       | `#167A5A` |
| Warning       | `#A96614` |
| Error         | `#B42318` |

Heatmap 使用：

```text
Viridis
```

优点：

* 色盲友好；
* 黑白打印时仍保留一定层次；
* 适合连续数值。

## 字体

为了隐私和加载稳定性，使用系统字体：

```css
font-family:
  system-ui,
  -apple-system,
  BlinkMacSystemFont,
  "Segoe UI",
  sans-serif;
```

RNA sequence、dot-bracket 和坐标使用：

```css
font-family:
  ui-monospace,
  SFMono-Regular,
  Menlo,
  Consolas,
  monospace;
```

## 卡片

```text
圆角：12 px
边框：1 px
阴影：非常轻
内边距：20–24 px
```

不要使用过强阴影。

---

# 十九、响应式设计

## 桌面端

```text
≥ 1,200 px
```

* 固定左侧栏；
* Contact Map 左图右详情；
* Structure 左设置右结果。

## 平板

```text
768–1,199 px
```

* 左侧栏折叠为图标；
* Contact Map 详情移到图表下方；
* Structure 设置面板变为顶部折叠区。

## 手机

```text
<768 px
```

页面仍允许：

* 上传；
* 查看 Summary；
* 搜索 interactions；
* 查看已有结构。

但 Contact Map 和大型结构预测显示提示：

```text
A larger screen is recommended for this analysis.
```

导航改为：

```text
Overview · Interactions · Map · Region · Structure
```

放到底部横向滚动栏。

---

# 二十、无障碍设计

必须包含：

* 所有按钮可键盘操作；
* 文件拖放区也可按 Enter 打开文件选择；
* 图表信息不能只依靠颜色；
* Heatmap 提供数据表替代视图；
* SVG nucleotide 支持键盘选中；
* 所有表单有 label；
* 错误信息使用文字和图标，而不仅是红色；
* 支持 `prefers-reduced-motion`；
* 正文与背景对比度符合 WCAG AA；
* 加载状态使用 `aria-live`。

---

# 二十一、空状态与错误状态

## Contact Map 未选择 RNA

```text
Choose two RNAs to generate a contact map.
```

## RNA pair 没有数据

```text
No interactions were found between U6 and U1.

[ Choose another pair ]
```

## Structure 没有 FASTA

```text
Reference sequence required

Add a FASTA file to fold a selected RNA region.

[ Add reference FASTA ]

You can also fold the sequence stored in an individual HYB record.
```

## Folding 引擎失败

```text
The folding engine could not complete this sequence.

Possible reasons:
• Sequence is too long
• Insufficient browser memory
• Unsupported sequence characters

[ Review sequence ] [ Reduce region ] [ Retry ]
```

## 浏览器不支持 WebAssembly

```text
RNA structure prediction is not available in this browser.

Interaction analysis and contact maps remain available.
```

---

# 二十二、建议的组件结构

```text
src/
├── app/
│   ├── App.tsx
│   ├── router.tsx
│   └── store.ts
│
├── pages/
│   ├── LandingPage.tsx
│   ├── OverviewPage.tsx
│   ├── InteractionsPage.tsx
│   ├── ContactMapPage.tsx
│   ├── ViewpointPage.tsx
│   ├── RegionExplorerPage.tsx
│   ├── ComparePage.tsx
│   ├── StructurePage.tsx
│   ├── MethodsPage.tsx
│   └── PrivacyPage.tsx
│
├── components/
│   ├── AppHeader.tsx
│   ├── AppSidebar.tsx
│   ├── FileDropzone.tsx
│   ├── ParsingProgress.tsx
│   ├── ValidationSummary.tsx
│   ├── RNASelector.tsx
│   ├── RegionInput.tsx
│   ├── InteractionTable.tsx
│   ├── RecordDrawer.tsx
│   ├── ContactHeatmap.tsx
│   ├── PartnerBarChart.tsx
│   ├── StructureViewer.tsx
│   ├── FilesDrawer.tsx
│   └── ExportMenu.tsx
│
├── hyb/
│   ├── types.ts
│   ├── parser.ts
│   ├── validator.ts
│   ├── count.ts
│   ├── indexes.ts
│   ├── filters.ts
│   ├── contactMap.ts
│   ├── regions.ts
│   └── export.ts
│
├── fasta/
│   ├── parser.ts
│   ├── matcher.ts
│   └── extract.ts
│
├── folding/
│   ├── engine.ts
│   ├── vienna.worker.ts
│   ├── constraints.ts
│   ├── dotBracket.ts
│   └── ctExport.ts
│
└── workers/
    └── hyb.worker.ts
```

---

# 二十三、完整用户操作流程

```text
1. 用户打开 HYB2 Web
2. 拖入 sample.hyb
3. 浏览器验证并解析
4. 显示 validation summary
5. 自动进入 Overview
6. 用户查看 Top RNAs 和 RNA pairs
7. 点击 U6–U2
8. 自动进入 Interactions 或 Contact Map
9. 在 heatmap 框选 U6:100–300 / U2:380–520
10. 点击 Explore region
11. 上传 reference.fa
12. 系统完成 RNA 名称匹配
13. 选择 U6:100–300
14. 点击 Send to RNA Structure
15. 选择 ViennaRNA Plain MFE / manual hard pairs / HYB-guided RNAcofold，或 CPLfold pseudoknot engine
16. CPLfold HYB-guided 时选择不超过 75 nt 的 single reference region、beam、phase-1 candidate 数、energy model、alpha 和 beta
17. 独立 Worker 执行 ViennaRNA evidence chain，或 Pyodide 内执行 HYB block bonus matrix 与 two-phase CPLfold
18. 显示结构、dot-bracket、energy、evidence 来源、bonus matrix 和可切换 candidate ranking
19. 用户下载 SVG、PNG、CT、DBN、base-pair/evidence/bonus/candidate TSV 和 report JSON
20. 可选加载多个 `.hyb`，查看 descriptive comparison map，并导出兼容现有 HYB2 R 入口的 count/names 表及独立扩展 metadata
21. 点击 Clear session
```

---

# 二十四、V1 功能边界

第一版建议正式支持：

```text
✓ 本地加载 .hyb
✓ 格式验证和错误报告
✓ Overview
✓ RNA partner 分析
✓ interaction 筛选和虚拟表格
✓ 单 RNA 和双 RNA contact map
✓ Viewpoint selected-coordinate / full mapped-reference 导航
✓ Region Explorer
✓ descriptive multi-file comparison
✓ 过滤结果导出
✓ 可选 reference FASTA
✓ 单 RNA reference-region MFE folding
✓ expert manual hard-base-pair constraints
✓ HYB-guided RNAcofold evidence 和 ranked constraints
✓ seeded randomized folding（最多 1,000 folds）
✓ COMRADES score 和 evidence-coloured structure
✓ CPLfold pure-Python/Pyodide pseudoknot candidates（浏览器上限 75 nt）
✓ HYB interval-block bonus matrix 可视化与导出
✓ HYB2 `DESeq_run.R`-compatible count / headerless names table 导出
✓ 独立扩展 sample metadata 导出
✓ SVG / PNG / CSV / HYB / DBN 导出
✓ 全程本地处理
```

第一版不承诺：

```text
✗ Bowtie2 mapping
✗ FASTQ/SAM 输入
✗ 浏览器内 DESeq2 significance（等待 pinned DESeq2 1.52.0 WASM closure、静态加载和数值门禁）
✗ 超过 75 nt 的浏览器内 CPLfold（保留本地 CLI）
✗ 云端保存
✗ 用户账户
✗ 分享链接
```

---

# 二十五、实施顺序

建议按以下顺序开发：

### 第一阶段：数据基础

```text
Landing
File Drop
HYB Parser
Validation
Web Worker
Overview
```

### 第二阶段：轻量分析

```text
Interactions
Filtering
Virtual Table
Record Details
CSV/HYB Export
```

### 第三阶段：核心可视化

```text
Contact Map
Viewpoint
Region Selection
Region Explorer
Descriptive multi-file comparison
SVG/PNG/TSV Export
Bundled AWK parity tests
```

### 第四阶段：结构预测

```text
FASTA Parser
Reference Matching
Sequence Extraction
ViennaRNA WebAssembly
RNAcofold evidence aggregation
Ranked / randomised constraint fitting
COMRADES support scoring
Structure Viewer
DBN/CT/SVG/Evidence/Ensemble Export
```

### 第五阶段：科研完善

```text
File SHA-256
Analysis parameters JSON
Methods documentation
Local IndexedDB sessions
Offline PWA
Pinned WebR DESeq2 dependency image
Native-R / WebR numerical parity gate
In-browser replicate-aware DESeq2
```

---

# 二十六、最重要的验收标准

1. 用户选择 `.hyb` 后，文件内容不会通过网络发送。
2. 页面在解析期间保持可响应。
3. 无效行不会导致整个文件分析失败。
4. Contact Map 默认使用 10 nt bin。
5. Contact Map 计算结果通过交叉执行测试与 bundled `bin/plot_hybrids_3.awk` entire-hybrid fixtures 一致。
6. 两 RNA arm 顺序规范化规则明确且可关闭。
7. 表格能流畅浏览至少十万条记录。
8. 无 FASTA 时，Structure 页面不会误导用户把 hybrid read 当作完整 RNA。
9. 结构结果明确标注算法、温度和限制。
10. 每类派生结果下载都提供配套的参数或 report JSON，其中包含分析参数、输入文件指纹和应用版本；PNG/CSV/TSV 等文件本身不必内嵌 metadata。
11. 清除会话后，内存和 IndexedDB 中的数据都被删除。
12. 网站刷新或关闭时，不发生隐式上传或云端保存。

最终首页的核心信息应保持极其简单：

```text
HYB2 Web

Open your HYB file.
Explore RNA interactions locally.
Predict RNA secondary structures with an optional reference FASTA.

[ Choose HYB file ]

Your data never leaves your device.
```

而分析工作台则围绕一条明确路径设计：

```text
HYB 文件
→ 数据概览
→ 选择 RNA interaction
→ Contact Map
→ 选择区域
→ RNA Structure
→ 下载科研结果
```
