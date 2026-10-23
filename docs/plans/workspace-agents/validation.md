# 计划与设计稿验证记录

日期：2026-10-23。范围：本计划文档和本地交互设计稿。Workspace／Agent／摄取／权限等正式产品功能仍待实施。

## 1. 产物核对

- 总计划明确 15 项核心决策和完整交付边界。
- 架构覆盖身份、所有权、Integration 授权、可见分区、主体、并发、generation、缓存、源撤回和派生记忆。
- 接入契约覆盖旧客户端、事件模式、独立记忆模式、三种入站协议、HTTP／gRPC／GraphQL 的共同语义。
- Manager 文档覆盖页面、路由、作用域切换、草稿、冲突、失败态、移动端、焦点和服务端会话边界。
- 实施计划包含 P0–P6 依赖顺序、迁移／回退和 A01–A22、U01–U12 验收编号。
- AGENTS 与中英文 README 已增加计划入口，并明确新增能力未实现；既有未提交产品代码未由本次计划工作修改。
- `.agents/plan/workspace-agents.md` 是本地入口；正式计划位于可被 Git 跟踪的 `docs/plans/workspace-agents/`。

文档检查包含相对文件链接、JSON 示例可解析性、验收编号完整性和新增文档的空白错误。现有项目测试矩阵写入 delivery.md，本次未执行 Go／Atlas／正式 Manager E2E。

## 2. 设计稿实际浏览器检查

使用仓库已有 Playwright 1.58.2，Node 24.19.0，匹配的 Chromium 145.0.7632.6 和 WebKit 26.0。浏览器安装在本任务独立缓存，不改变 package manifest 或 lockfile。

| 检查 | 结果 |
| --- | --- |
| Chromium：320／390／768／1280／1440px × 8 页面 | 40 组无页面横向溢出 |
| WebKit：相同宽度 × 8 页面 | 40 组无页面横向溢出 |
| 搜索 → 详情 → 编辑 → 来源消息 | 两个浏览器通过 |
| 工作区切换 → 成员画像 → 自述／推断分区 | 两个浏览器通过 |
| Agent 能力修改 → 页面有效能力更新 | 两个浏览器通过，操作只作用于示例 |
| 390×500 短屏编辑、移动导航切换、Escape 后焦点恢复 | 两个浏览器通过 |
| 浏览器 JavaScript runtime errors | 0 |

自动化共 18 组结果，布局组合 80 组，机器可读记录见 [checks.json](preview/checks.json)。检查脚本为 [verify.mjs](prototype/verify.mjs)。WebKit 首次检查发现导航关闭后焦点没有返回菜单按钮，设计稿已补显式恢复并通过复验。

已目视检查 [桌面记忆详情](preview/desktop.png) 和 [手机成员画像](preview/mobile.png)。桌面保留列表与来源 inspector；窄屏使用独立详情，主要操作可触达，人物自述和模型推断有文字区分。

## 3. 可复验入口

先按 [总计划](README.md) 启动静态预览，再使用 Node 24 运行：

```sh
rtk proxy node docs/plans/workspace-agents/prototype/verify.mjs
```

若使用独立浏览器缓存，设置 `PLAYWRIGHT_BROWSERS_PATH` 为安装目录；预览地址可用 `MEMSTATION_PLAN_PREVIEW_URL` 覆盖。`MEMSTATION_PLAN_BROWSERS=chromium` 或 `webkit` 可仅检查单个引擎，完整结果需要两者。截图和报告均由脚本生成。

## 4. 证据边界

设计稿没有真实数据库、认证、模型、后台任务或跨进程并发。页面内“保存权限”仅验证交互，不能证明权限隔离正确。尚未覆盖真实设备软键盘、完整屏幕阅读器、200% 浏览器缩放、暗色主题、真实后端长列表和全部最终业务页面；这些仍由 delivery.md 的正式验收矩阵要求。

本次没有宣称生产性能、真实模型画像准确率、远端 CI、已提交／已部署或最终 v1 完成。计划完成的交付内容是可执行文档、可编辑设计稿及其局部交互证据。
