# 桌面版改用 MyGo 原生界面：迁移计划

日期：2026-10-14。决定：桌面版改用 [MyGo](https://mygo.egoist.dev)（Go，0.4.8，MIT）的**原生界面**，不再使用 Tauri 和 WebView。网页版 `web/` 不受影响。

## 1. 范围

| 层 | 现在 | 迁移后 |
|---|---|---|
| 外壳 | Tauri 2（Rust，`desktop/src-tauri`，约 3500 行，24 个命令，34 个测试） | Go + MyGo（`desktop/app`），单个 Go 程序，不用 cgo |
| 界面 | WebView 里的 `desktop/ui`（约 4000 行 JS），Explore 和 Report 通过 `Hyb2Web.mount` 嵌入网页版页面（`web/analysis-*.js`、`report-page.js` 等约 6000 行） | MyGo `ui` 包画的原生界面，全部用 Go 重写 |
| 计算 | 捆绑的 Python 3.11 + numpy/numba + Bowtie2，`desktop/runtime/bridge.py` | **不变**。仍是两个常驻 sidecar（行分隔 JSON，契约 C1）加每个长任务一个子进程 |
| 项目格式 | `project.json` schema 2.0 | **不变** |

结果：桌面版和网页版从共用分析页面变成两套界面代码，但共用同一个 Python 后端和项目格式。桌面版因此不需要 WebView：启动更快、内存更少，Windows 上也不依赖 WebView2，Linux 上不依赖 WebKitGTK。

## 2. 已核实的 MyGo 能力（docs 和 examples，提交 f2eca9f）

| 需求 | MyGo 对应能力 |
|---|---|
| 大面积热图（contact map、差异图，几十万个格子） | `ui.Canvas` 在 GPU 上离屏渲染一次，之后每帧只画一张图；配合 `Painter.Transform` 做缩放和平移 |
| 弧图、柱状图、覆盖度曲线 | `Painter` 的路径和线条；另有 `chart` 组件 |
| 几十万行的表格 | `ui.Table` 只构建可见行，支持排序、调整列宽、多选 |
| 选择文件、文件夹，保存文件 | `mygo.Dialog.Open/Save/Message` |
| 捆绑 Python 运行时 | 配置项 `resources` 加按平台区分的资源目录；运行时用 `App.Path(mygo.PathResources)` 找到 |
| 子进程（sidecar、任务、取消整棵进程树） | Go 标准库 `os/exec`，Unix 用进程组，Windows 用 Job Object 或 `taskkill /T` |
| 深色模式 | `mygo.Theme` 加主题的颜色变量 |
| 打包 | `mygo build -platform darwin/universal,windows/amd64,linux/amd64`；macOS 签名和公证，Windows 用 NSIS 安装包 |
| 测试 | `docs/ui/testing.md`：可以不开窗口驱动界面并断言 |

要求：Go ≥ 1.27（本机还没装，Homebrew 上是 1.27.2）。风险：MyGo 版本还很早，API 可能变动，见第 6 节。

## 3. 目标结构

```
desktop/app/                 Go 模块（新）
  main.go                    App.Run、窗口、菜单
  mygo.json                  名称、identifier、resources（native/ 运行时）、平台资源
  core/
    sidecar.go               C1 行协议客户端：串行化、超时后杀掉重启、记录 stderr 尾部
    tasks.go                 预算队列、status.json、取消进程树、resume、interrupted 标记
    prefs.go                 preferences.json（预算、主题）
    ops.go                   project_op 和 analysis_query 的白名单（与 main.rs 一致）
    *_test.go                移植现有 34 个 Rust 测试，另加真实 bridge 往返
  model/                     项目、样本、任务快照（对应 project.schema.json）
  views/
    shell.go                 侧栏导航（① 数据 ② 质控 ③ 探索 ④ 比较 ⑤ 结构 ⑥ 报告）、顶栏当前选择、任务抽屉、偏好设置、帮助
    start.go                 打开、新建、最近项目
    data.go                  参考序列、样本表、导入样本表（先预览）、一键运行、磁盘预估、续跑
    qc.go                    统计表
    explore_*.go             Interactions 表、Contact map（Canvas）、Viewpoint、Region
    compare.go               DESeq2 任务、结果列表、显著 bin 图、interaction 表、Quick view
    structure.go             折叠表单、结果列表、弧图、A/B 对比
    report.go                汇总加导出（SVG、PNG、TSV、HTML 文件，由 Go 生成）
    widgets/                 heatmap、arcs、legend、progress 等共用控件
```

Python 端只加一个东西：Explore 原来在 JS 里算的几项统计（viewpoint 覆盖度、region 详情）如果后端还没有对应查询，就在 `hyb2analysis.query` 里补上。原则是数据全在 Python 里算，Go 只负责显示。

## 4. 阶段

| 阶段 | 内容 | 验收 |
|---|---|---|
| **M0 骨架** | 安装 Go 和 mygo CLI；`desktop/app` 起一个原生窗口；把 `desktop/resources/native` 配成资源；Go 端调用一次 bridge 的 `runtime_detect` | `mygo build` 生成的 .app 能启动，显示运行时检测结果 |
| **M1 核心移植** | sidecar、任务调度、偏好设置、白名单、对话框，逐项对应 Rust 版 | 34 个 Rust 测试的 Go 版本全部通过；真实 bridge 往返；取消任务时整棵进程树被杀掉 |
| **M2 外壳、数据、任务** | 导航、顶栏、任务抽屉、开始页、数据页、质控页、偏好设置 | 从空项目开始：导入样本表 → 运行 → 进度 → 质控表，和现在的 GUI 验收步骤一致 |
| **M3 探索** | Contact map（Canvas，缩放、点击格子、"折叠这个区域"）、Interactions 表（筛选、排序、几十万行）、Viewpoint、Region | 640 万条杂交体的 ZSIN_REP3：contact map 和筛选的响应 ≤ 1 s（G4），帧率流畅 |
| **M4 比较和结构** | 比较页（DESeq2 任务、显著 bin 图、interaction 表、定位、Region、A/B 两次折叠），结构页（表单、弧图、A/B 对比） | AFR vs BRA 得到 82,132 个显著 bin 和 742 / 1,547 条 interaction；全基因组弧图 3654 对 |
| **M5 报告、打包、收尾** | 报告页和导出；深色模式；无障碍；三平台 CI 构建和打包；删除 `desktop/src-tauri` 和 `desktop/ui`；更新文档 | 三平台安装包都能构建；端到端从原始数据到结构的流程在打包好的 app 里跑通 |

M1 完成后，M2、M3、M4 可以由多个 agent 在各自的 worktree 里并行开发，彼此只通过 `core/` 和 `widgets/` 的接口依赖。

## 5. 验证方式

- **Go 单元测试**：`go test ./...`，覆盖 core 和 model。
- **界面测试**：用 MyGo 的测试工具驱动界面，对每个页面的关键路径做断言，不依赖屏幕录制权限（终端目前没有这个权限，真实窗口看不到）。
- **端到端**：用打包好的 app 和真实 ZAFR、ZBRA 数据跑一遍，与 reproduce_zikv 对比。
- **Python 测试和网页版验证脚本**：继续在 CI 里运行，不受影响。

## 6. 风险

- **MyGo 还很早（0.4.x）**：遇到缺失或有 bug 的组件时，先自己写一个 custom widget 绕过，不改 MyGo 源码；把用到的 MyGo 版本锁定在 `go.mod` 里。
- **重写分析页面的工作量**：网页版的 Explore 和 Report 约 6000 行，是工作量最大的部分（M3、M5）。
- **两套界面会慢慢出现差异**：网页版以后加的功能，桌面版要另外实现。缓解办法是让统计逻辑尽量放在 Python 后端，两边都只负责显示。
- **Tauri 的保留时间**：建议在 M4 验收之前，保留 Tauri 版本作为可运行的对照；M5 时一起删除。

## 7. 需要确认

1. 安装 Go 1.27.2（`brew install go`）和 mygo CLI（`go install`，MyGo 的 getting-started 文档里写的方式）。
2. Tauri 版本保留到 M5 再删（建议），还是现在就删。
3. 按 M0 → M1 顺序做，M1 完成后用多个 agent 并行做 M2、M3、M4。

## 8. 进度（2026-10-14）

M0 到 M5 都已完成，Tauri 外壳（`desktop/src-tauri`）和旧的 WebView 界面（`desktop/ui`、`package.json`、`stage-frontend.cjs`）已从仓库中删除。

- **M0**：`desktop/app` 骨架，捆绑运行时作为平台资源打包。
- **M1**：Rust 核心移植到 Go（`core/`：sidecar、任务调度、偏好设置、白名单），Rust 测试全部移植。
- **M2–M4**：数据、质控、探索、比较、结构页面，以及共享的热图、弧图和覆盖度控件；探索页新增后端查询 `analysis_viewpoint` 和 `analysis_region`。
- **M5**：报告页和 HTML 报告导出，各页面的 TSV / SVG 导出，三平台 CI（`.github/workflows/desktop-native.yml`），文档更新。
- **验证**：`e2e_test.go` 用真实 ZIKV 数据跑通“导入 → 流程 → 质控 → 差异检验 → contact map → 折叠”（约 100 秒），记录见 `desktop/VERIFICATION.md`。

仍未完成：Windows 和 Linux 上实际运行 APP（CI 只覆盖构建和测试）；在 macOS 上手动点击真实窗口（本机终端没有屏幕录制权限）；签名和公证。`web/desktop-adapter.js` 里的 Tauri 分支对网页版无影响，留待网页版下次整理时清理。
