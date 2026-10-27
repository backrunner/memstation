# Manager 共享 UI 组件

Manager 的筛选、记录元数据、身份详情、任务表单、聊天工具和调试信息统一使用 `apps/manager/src/lib/components/ui/CollapsibleSection.svelte`。不要在页面重新写原生 `details/summary` 或局部展开按钮样式。

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

## 回归检查

浏览器回归应覆盖收起表单提交、SSR 恢复、键盘与焦点、无效字段自动展开、
减少动态效果以及桌面和窄屏布局。fixture 验证不等同于真实后端或物理手机验收。
截图、检查计数和每轮结果保存到本地忽略目录。

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

## 组件目录

`ui/` 放表单控件、折叠和确认等基础交互；`layout/` 放页头、筛选栏和栏目导航；`content/` 放 Markdown、代码和 JSON；`charts/` 放图表。记忆、Agent、工作区、配置、Target 与聊天组件分别放入对应业务目录。统一使用 `#lib/components/<分类>/<组件>.svelte`，基础组件不得导入业务组件。
