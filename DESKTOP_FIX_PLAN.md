# HYB2 Desktop 修复计划（2026-10-13）

基于 APP 架构审查的 15 个问题。本轮由 3 个并行 agent 按**文件所有权**分工修复；
跨层接口以本文件的「接口契约」为准，任何 agent 不得修改不属于自己的文件。

源码备份：`scratchpad/backup-before-fix/desktop-web.tgz`（hyb2 不是 git 仓库）。

## 范围

| # | 问题 | 本轮 | 负责 |
|---|---|---|---|
| 1 | Windows 优先 vs. bash/perl/awk/R pipeline | **不改**，需要产品决策（WSL / conda-MSYS / 重写 Python） | 用户 |
| 2 | 分析页未加载 `desktop-adapter.js`；saveFile/openFile 空实现 | ✅ | C（前端）+ A（保存对话框命令） |
| 3 | 原生 CPLfold 被 `min(max_nt, 500)` 限制 | ✅ | B（后端）+ C（前端读取原生上限） |
| 4 | 每次 IPC 冷启动 Python，Numba JIT 不复用 | ✅ 常驻 sidecar | A + B |
| 5 | 四套任务机制、无持久化/恢复 | ✅ 统一为 Rust 任务模型 | A + B + C |
| 6 | `project.json` 无锁、非原子写 | ✅ | B |
| 7 | 大文件经 JSON 管道传输 | ✅ asset protocol | A + C |
| 8 | 桌面路由与网页路由共用 `ui_state.route` | ✅ 命名空间拆分 | C |
| 9 | 网页 CSS/全局对象污染桌面外壳 | ⚠️ 部分：桌面外壳样式加前缀并去掉元素选择器依赖；完全隔离留到后续 | C |
| 10 | CSP=null + innerHTML 未转义 → 任意文件外泄 | ✅ | A（CSP/capabilities/去掉任意路径导入）+ C（转义） |
| 11 | release 也信任 `HYB2_REPOSITORY_ROOT`/`HYB2_PYTHON` | ✅ 仅 debug | A |
| 12 | FASTQ/SAM pipeline 未实现 | **不改**，依赖 #1 | — |
| 13 | 手输路径、无对话框、JSON 直接显示、无最近项目 | ✅ | A（对话框命令）+ C（UI） |
| 14 | 版本号四处重复 | ✅ | A + B |
| 15 | 约 1 GB 的 `resources/native-*` 备份、开发机绝对路径 | **不删**，需用户确认 | 用户 |

## 文件所有权

- **Agent A — Rust 外壳**：`desktop/src-tauri/**`（`Cargo.toml`、`tauri.conf.json`、`src/*`、新增 `capabilities/*`）、`desktop/package.json`
- **Agent B — Python 运行时**：`desktop/runtime/**`、`desktop/task-runner/**`、`desktop/spec/**`、`web/cplfold/cplfold_web.py`
- **Agent C — 前端**：`desktop/ui/**`、`desktop/scripts/stage-frontend.cjs`、`web/desktop-adapter.js`、`web/structure-controller.js`、`web/analysis-pages.js`、`web/app.js`（仅限 `Hyb2Web` 桥接部分）
- 主会话：本计划、`desktop/README.md`、集成验证

网页版必须保持可用：对 `web/*` 的修改在没有 `__TAURI__` 时行为不变。

## 接口契约

### C1. 常驻 sidecar 协议（A ↔ B）

- 启动：`python -m desktop.runtime.bridge --repo-root <root> --serve`
- stdin 每行一个请求：`{"id": <int>, "request": {...}}`
- stdout 每个请求恰好输出一行：`{"id": <int>, "response": {...}}`；`response` 就是 `handle()` 的返回值，异常时为 `{"ok": false, "error": "..."}`。
- stdout 只能用于协议；处理请求期间的其他输出必须重定向到 stderr。stdin EOF 时以 0 退出。
- 保留现有的单次模式（stdin 一行、`--request`、`--validate-project`），供任务子进程和测试使用。
- Rust 维护**两个** sidecar：`control`（项目操作、detect、register_task_run）和 `compute`（`cplfold_predict`）。每个 sidecar 用 Mutex 串行处理请求；崩溃或 EOF 后下一次请求自动重启；响应超时（control 120 s，compute 900 s）时杀掉并重启。
- Rust 为 sidecar 设置的环境变量：`HYB2_APP_VERSION=<CARGO_PKG_VERSION>`；compute sidecar 额外设置 `NUMBA_CACHE_DIR=<app_cache_dir>/numba`。B 在设置了这些变量时使用它们。

### C2. project.json 写入（B）

- 所有写入都是 读→改→写临时文件→`os.replace`，并持有 `project.json.lock` 上的文件锁（POSIX 用 `fcntl.flock`，Windows 用 `msvcrt.locking`），在锁内重新读取。
- 新增 bridge 操作 `register_task_run {path, task_id}`：读取 `runs/<task_id>/status.json`（以及存在时的 `result.json`），在 manifest 的 `runs` 中追加或更新一条记录（`id=task_id`、`kind`、`status`、相对 `output_paths`/`log_paths`）。重复调用必须幂等。

### C3. 统一任务 API（A ↔ C，B 提供子进程）

Tauri 命令（删除 `task_run` 和 `cplfold_run`）：

- `task_start({request:{project_path, kind:"validate_project"|"cplfold", sequence?, bonus_matrix?, parameters?, task_id?}})` → 快照
- `task_status({request:{task_id, project_path?}})` → 快照；内存中没有时从 `<project>/runs/<id>/status.json` 读取
- `task_cancel({request:{task_id}})` → 快照
- `task_list({request:{project_path}})` → `{ok:true, tasks:[快照…]}`，最新的在前。状态为 `running` 但当前没有存活进程的，标记为 `interrupted` 并持久化。

快照字段：`ok, task_id, kind, status, exit_code, error, run_dir, log_paths, started_at, finished_at, output`。
`status` ∈ `queued|running|succeeded|failed|cancelled|interrupted`。

- `run_dir/status.json` **只由 Rust 写**。任务子进程（`bridge --request task-request.json`）把自己的结果写到 `run_dir/result.json`（B 需要修改 `cplfold_run` 和 `run_task`）；Rust 结束时从 `result.json` 读取 `output`。
- 任务进入终态后，Rust 通过 control sidecar 调用 `register_task_run`。

### C4. 对话框与文件（A ↔ C）

- `dialog_pick_project_folder()` → `{path: string|null}`
- `dialog_pick_new_project_folder()` → `{path: string|null}`
- `project_import_input_dialog({request:{path, role}})` → Rust 打开文件选择器后调用 bridge `import_input`；返回 `{ok, input}`，用户取消时返回 `{ok:true, cancelled:true}`。**删除**接受任意源路径字符串的 `project_import_input` 命令。
- `file_save_dialog({request:{suggested_name, data_base64}})` → `{saved: bool, path?: string}`
- Asset protocol：`project_open` 成功后，Rust 把 `<project>/inputs` 和 `<project>/runs` 递归加入 asset scope。前端用 `fetch(__TAURI__.core.convertFileSrc(absPath))` 读取输入，失败时回退到 `project_read_input`。

### C5. CSP 与 IPC 权限（A；C 必须遵守）

- CSP：`default-src 'self'`；script 只允许 `'self'`，以及 Pyodide 和 WASM 需要的 `'wasm-unsafe-eval' 'unsafe-eval'`；不允许内联 `<script>`，也不允许内联事件属性；style 允许 `'self' 'unsafe-inline'`；`connect-src` 只允许 `ipc:`、`http://ipc.localhost`、`'self'` 和 asset 协议；img 允许 `'self' data: blob:` 和 asset 协议；worker 允许 `'self' blob:`。
- `capabilities/main.json` 只授权给 `main` 窗口，只放必要的 core 和 dialog 权限。
- C：所有来自 manifest 或运行时的数据都用 `textContent` 或转义后再写入页面。

### C6. ui_state 结构（C；B 的校验必须接受）

`{ "desktop": {"route": "home|analysis|tasks|runtime"}, "analysis": <Hyb2Web.getSessionSnapshot()> }`。
遗留的扁平 `{route, ...}`：如果 `route` 是桌面视图名就放进 `desktop`，否则放进 `analysis`。传给 `Hyb2Web.restoreSession` 的只有 `analysis`。

### C7. 原生 CPLfold 长度上限（B ↔ C）

- B：`cplfold_predict` 使用 `maxSequenceLength = max_nt`（`HYB2_CPLFOLD_MAX_NT`，默认 1000），不再取 `min(…, 500)`；同时检查 `cplfold_web.py` 内部是否另有硬编码的 500。
- B：detector 输出的能力项 `structure.cplfold_native` 包含 `details.max_sequence_length`。
- C：`desktop-adapter.js` 读取该值并暴露为 `Hyb2Desktop.limits.cplfoldMaxLength`；`structure-controller.js` 在原生路径下使用这个上限，浏览器路径仍用 500。

### C8. 版本号（A + B）

`tauri.conf.json` 的 `version` 指向 `../package.json`；Rust 用 `CARGO_PKG_VERSION`（Cargo.toml 与 package.json 手动同步，由 A 加一个测试或构建检查）。B：`new_project` 优先使用 `HYB2_APP_VERSION`。

## 验收标准

- `python3 -m unittest discover -s desktop/runtime/tests -p 'test_*.py'`、`desktop/task-runner` 测试和 `desktop/spec/test_protocol_contract.py` 全部通过，并补充新测试（serve 模式、原子写/锁、register_task_run、ui_state 嵌套结构）。
- `cargo test` 和 `cargo build` 在 `desktop/src-tauri` 下通过；新增测试覆盖 sidecar 协议往返、task_list 把 interrupted 正确标出、release 构建不读取环境变量覆盖。
- `npm run stage` 通过；`node --check` 所有修改过的 JS 文件都通过；网页版在没有 `__TAURI__` 时不报错。
- 集成：主会话最后运行全部测试，并核对各契约在两侧的实现一致。

## 进展记录（2026-10-13）

- 三个 agent 的修复都已合入：Python 测试 32 个通过（1 个因缺 numpy 跳过）+ 3 + 3，`cargo test` 17 个通过，clippy 无警告，`npm run stage` 和 `node --check` 通过。
- 集成时发现：`desktop/resources/native` 里的源码是旧版本（没有 `--serve`）。已用 `stage-native.py` 重新打包；旧目录改名为 `resources/native-pre-fix-20261013`，没有删除。
- `verify-native-release.py` 新增一项检查：打包进去的源码必须和仓库源码的哈希一致，防止再次发布过期的 bridge。
- `tauri build --debug --bundles app` 成功；在严格 CSP 下，Project Home 界面正常渲染。
- 端到端测试（直接驱动 .app 内的 sidecar）：
  - detect 返回 `cplfold_native` 可用，`max_sequence_length=1000`；
  - 600 nt 原生预测成功（修复前会被 500 nt 上限拒绝）；
  - 1200 nt 被正确拒绝。
- 未完成：GUI 点击流程没有验证，因为终端没有辅助功能权限。
- 新发现的问题：600 nt 原生 CPLfold 首次运行 164 s，同一进程第二次仍要 117 s。JIT 复用只省了约 47 s，主要耗时在算法本身（CPLfold 的 two-phase pseudoknot），需要单独做性能分析。

## #1 / #12 决定：pipeline 改写为 Python（2026-10-13）

- 已删除 6 个旧的 `resources/native-*` 目录（约 1 GB）。
- 新增 `pipeline/hyb2pipe`（只依赖标准库）：
  - 覆盖 `hyb2` 第 1 步（FASTQ/FASTA/SAM → .hyb）；
  - 在 `data/testData.sam` 上，12 个阶段都与旧脚本逐字节一致；
  - 耗时从 27 s 降到约 5 s。
- 桌面集成：
  - 新增任务类型 `pipeline`：Rust → bridge 的 `pipeline_run` → `hyb2pipe`；
  - 运行时检测新增 `pipeline.python`；
  - 任务中心可以选择输入和参考序列来运行，完成后能一键在分析页打开结果；
  - pipeline 已打包进 native 运行时，`verify-native-release` 也会检查它的源码是否过期。
- 还缺：
  - Windows 安装包没有带 Bowtie2 二进制（FASTQ 路径需要它）；
  - `-e 1` 需要 ViennaRNA，而它在 Windows 上没有确定可用的 wheel；
  - `hyb2_compare`（R/DESeq2）没有移植；
  - 没有在 Windows 上实际跑过测试。

## Bowtie2 打包与跨平台 CI（2026-10-13）

- `desktop/scripts/acquire-bowtie2.py`：
  - 按固定版本下载 Bowtie2 2.5.5，用 GitHub 公布的 SHA-256 校验；
  - 只保留 `bowtie2-align-s` 和 `bowtie2-build-s`，加上 GPL-3.0 许可证；
  - Windows 版只依赖 KERNEL32 和 msvcrt。
- `stage-native.py --bowtie2`：
  - 打包时做冒烟测试（建索引并比对一条 read）；
  - 没有 Bowtie2 时 `verify-native-release` 判为失败；
  - 源码一致性检查改为复用 `SOURCE_DIRS`。
- 检测器、`hyb2pipe` 和 bridge 都优先使用 bundle 里的 `bin/`。实测：PATH 里没有 bowtie2 时，APP 内的 FASTQ 任务能跑通，结果与旧流程一致。
- 新增 `.github/workflows/pipeline-crossplatform.yml`：在 Windows、Linux、macOS 上跑 pipeline 基准测试和 desktop runtime 测试，尚未推送运行。
- `.gitattributes` 把基准文件和测试数据标为 `-text`，防止 Windows checkout 时转成 CRLF。
- 遗留：Bowtie2 没有 macOS x86_64 官方版本；`-e 1` 需要的 ViennaRNA 没有打包；`hyb2_compare` 未移植。

## ViennaRNA 与 Rust 端到端测试（2026-10-13）

- Rust 端到端测试：`pipeline_task_runs_real_bridge_end_to_end`
  - 路径：Rust 任务模型 → 真实 bridge 子进程 → hyb2pipe；
  - 输出与旧流程逐字节一致，任务完成后会触发结果登记。
- ViennaRNA 2.7.2 已打包进 native 运行时：
  - 加入了 requirements 清单；打包时做冒烟测试；缺失时发布校验判为失败；
  - 运行时检测新增 `pipeline.energy`；
  - `-e 1` 实测：10411 条 hybrid 都有 dG，每行都是 18 列，耗时约 63 s（单线程）；
  - 能量结果没有和旧流程的 RNAcofold 对比过（本机没有 RNAcofold），两者用的是同一套 ViennaRNA 默认参数。
- 浏览器 GUI 走查中修复了 3 个问题：
  - `Hyb2Web` 启动顺序；
  - 18 列 sam2hyb 输出的解析；
  - 嵌入桌面端时的布局，以及资源路径。

## 原生 CPLfold 性能（2026-10-14）

- 原因：inside 动态规划已经用 `@njit` 编译，但次优结构要用的 outside 计算（`BeamCKYParserHyper._outside` 及其打分方法）是纯 Python，占了约 99% 的时间。
- 修复：把这些方法的源码原样生成为模块级的 `@njit(cache=True)` 函数（不开 fastmath）。设置 `CPLFOLD_PYTHON_OUTSIDE=1` 可以切回原来的 Python 实现，用于对比。
- 结果：在 28、80、150、300、600 nt 上输出都与原实现逐字节一致；网页版的纯 Python 模式结果也一致；`verify-cplfold-python.py` 通过。
  - 150 nt：13 s → 约 1 s
  - 300 nt：62 s → 约 1 s
  - 600 nt：270 s → 约 1 s
  - 1000 nt：3 s
- APP 内的 `cplfold_predict`（600 nt）：0.4 s。安装后第一次运行要做 Numba 编译，约 50 s；之后读磁盘缓存，约 1.6 s。
