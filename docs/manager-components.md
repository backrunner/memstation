# Manager 共享 UI 组件

Manager 的筛选、记录元数据、身份详情、任务表单、聊天工具和调试信息统一使用 `apps/manager/src/lib/components/CollapsibleSection.svelte`。不要在页面重新写原生 `details/summary` 或局部展开按钮样式。

## 用法

```svelte
<CollapsibleSection title="Record details">
  <JsonView value={record.metadata} />
</CollapsibleSection>

<CollapsibleSection title="Filters" count={activeCount || undefined} variant="toolbar">
  {#snippet icon()}<SlidersHorizontal size={16} />{/snippet}
  <!-- 普通表单控件；收起后值仍参与原生表单提交 -->
</CollapsibleSection>
```

| 属性 | 用途 |
| --- | --- |
| `title` | 必填标题，同时作为按钮的可访问名称 |
| `count` | 可选计数，统一排版为 `Title (2)`；不要在 title 中手工拼计数 |
| `description` | 可选说明，关联到按钮的可访问描述 |
| `icon` | 可选语义图标 snippet，图标不会重复读屏 |
| `open` | 默认收起，可 `bind:open`；操作中的组件可绑定 `disabled` |
| `variant` | `plain`：分隔线详情区；`panel`：独立面板；`toolbar`：紧凑筛选按钮 |
| `contentClass` / `class` | 业务布局扩展，不覆盖共享的展开、焦点或尺寸规则 |
| `header` | 把提供的 trigger snippet 放入组合工具栏；只渲染一次 |

`FilterToolbar` 已复用该组件。含搜索和主要操作的页面优先使用 `FilterToolbar`，独立折叠筛选可使用 `variant="toolbar"`。

## 设计与行为

- 标题、计数、图标与展开箭头统一对齐；展开使用浅绿色表面和深绿色强调，悬停与键盘焦点各有明确状态。详情使用分隔线，独立面板才有完整外框。
- 样式集中在组件中，移除 `app.css` 中的旧折叠定义和页面内 summary 样式。使用现有设计 token，无新增依赖。
- 继续使用 Bits UI 的 button、`aria-expanded`、`aria-controls` 和内容可见性；Enter／Space 可操作，收起内容 inert，不能通过 Tab 进入。
- 内容保留在 DOM，收起不重置表单或编辑器状态；原生 GET 表单仍提交隐藏区域的控件值。约束校验发现隐藏的无效控件时展开区域、聚焦控件并显示校验结果。
- 展开／收起动画受区域裁切，避免过渡期间压到后续内容；遵循 `prefers-reduced-motion`。移动触控至少 44px。

## 覆盖与验证

统一覆盖 13 个文件中的 22 处 CollapsibleSection 调用（包含 FilterToolbar），以及使用 FilterToolbar 的 6 个页面；将工作区历史筛选、Workspace details、Agent Identity details 的三处原生折叠替换为共享组件。聊天工具调用／结果、原始事件计数改用 `count`。Manager 源码中原生 `details/summary` 已清零。

浏览器回归覆盖收起表单提交、SSR 后刷新恢复、键盘与焦点、无效字段自动展开、减少动态效果、工作区／Agent／任务区域、桌面和 320／390px 布局。截图使用真实运行的 Manager 和本地 fixture，不能当作真实后端或物理手机验证。该调整不改变服务端接口或数据库。

2026-10-24 验证记录：

- Manager lint／Svelte 类型检查通过，0 errors、0 warnings；production build 通过。构建仍提示部分既有 chunk 大于 500 kB。
- Vitest：26 项通过，1 项需要独立 Redis 环境的集成测试跳过；本次未修改该链路。
- Playwright 全量 90 项（Chromium 57、mobile WebKit 33）：89 项通过，1 项旧测试的 `getByLabel('Filter')` 同时匹配输入框和 `Filters` 按钮。改为精确的 textbox role 后，该用例及新增折叠专项复测 5／5 通过；无剩余失败。
- 已检查桌面、320px／390px 截图，包含收起／展开筛选、工作区和 Agent 详情、任务面板；18 张截图和机器可读验证记录保存在本地 `.artifacts/manager-collapsible-20261024/`。


## 工作区选择与动线

`workspaces/WorkspacePicker.svelte` 用于服务器搜索和分页的工作区导航，基于 Bits UI Popover。普通表单下拉项继续用 `SelectField`。不要用普通按钮加字符箭头模拟 Select，也不要用居中大弹窗替代上下文切换。

- 工作区名称使用固定控件高度、Lucide 箭头和省略显示；当前 Owner 在上下文栏单独呈现。
- 面板贴近触发器，名称与 Owner 分行；当前项通过勾选和 `aria-current` 表达。选择当前项只关闭面板，保留详情、generation 和 URL。
- 打开后聚焦搜索，Enter 提交，Escape／外部点击关闭，关闭后恢复触发器焦点。工作区导航使用真实链接；切换到其他空间保留适用栏目／筛选并回到列表，未保存草稿沿用离开确认。
- 搜索、首屏／下一页、失败重试继续请求服务器，旧响应不能覆盖关闭后重新打开的面板。窄屏限制宽度，短屏滚动；不用遮罩阻断整个工作页面。

## 元数据与控件对齐

`MetadataRow.svelte` 接收 `items`，过滤空项，用可换行文本和留白组织并列信息。保留复制文本及可访问名称中的空格，不插入圆点、竖线或重复胶囊。

```svelte
<MetadataRow items={[`${record.message_count} messages`, `${record.total_tokens} tokens`, record.finalize_status]} />
```

说明文字使用自然语句；不要把全部圆点替换成另一种装饰符。搜索框和提交按钮底部对齐；Key Memory 窄屏搜索框占满一行，Apply 与 New key memory 在下一行对齐。沿用共享表单、按钮、Select、Dialog 和折叠组件的焦点、尺寸与触屏规则。

本次范围和实际验证结果见 [2026-10-24 UI 整理记录](plans/workspace-agents/manager-ui-refinement-2026-10-24.md)。

2026-10-24 工作区选择与元数据整理：98 项完整浏览器回归及审计加载态修正后 3 项复测通过，Manager lint／类型检查和 production build 通过；29 项单测通过，1 项独立 Redis 集成测试跳过。桌面／移动截图与实际边界见上述 UI 整理记录。
