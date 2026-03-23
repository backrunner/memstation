# MemStation - 管理面板详细实施计划

## 1. 文档定位

本文件用于细化 `apps/manager` 的实现方案。

- 以 `~/Projects/memstation/AGENTS.md` 为最高优先级
- 本文件补充管理面板的信息架构、交互模式、视觉语言和开发顺序
- v1.0 目标不是演示页，而是可用于日常运营、排障、审计和配置管理的正式管理面板
- 管理面板固定采用 `SvelteKit + Svelte 5 + Bits UI + shadcn-svelte`
- `Bits UI` 是唯一的一等 primitive 基础层；只有在 `Bits UI` 覆盖不到的极少数组件语义下，才允许引入兼容的 Radix 风格实现或本地补位
- 交互语义、组件组织和可访问性约定对齐 `Radix / shadcn` 设计语言，但不引入第二套并行设计系统

---

## 2. 管理面板目标

管理面板是 MemStation 的运营与排障入口，优先满足以下四类任务：

1. 日常观测：看请求、看错误、看队列、看记忆规模
2. 排障定位：查对话、查工具调用、查失败任务、查审计
3. 数据运营：查摘要、印象、事实、Key Memory，并做人工修正
4. 系统管理：修改模型配置、查看健康状态、处理队列积压、重试任务

v1.0 的核心要求：

- 默认支持单管理员、单实例自托管场景
- 但信息架构要能自然扩展到多用户、更多角色和更复杂的运维面
- 所有关键读操作可快速定位
- 所有关键写操作必须带审计感知
- 不把管理密钥暴露到浏览器长期存储
- 能连接任意指定的 MemStation 后端，而不是绑定单一内网实例
- 支持后续 cluster / multi-instance / region 化扩展

非目标：

- 不做面向普通终端用户的聊天 UI
- 不做营销官网风格页面
- 不做以动画展示为主的低信息密度 dashboard

---

## 3. 技术与工程基线

## 3.1 固定技术栈

以下版本以 2026-03-15 实测 `npm view` 为准：

| 类别 | 选型 | 用途 |
|------|------|------|
| App framework | `@sveltejs/kit@2.55.0` | 路由、页面组织、部署壳层 |
| Runtime | `svelte@5.53.12` | 组件运行时 |
| UI primitives | `bits-ui@2.16.3` | 可访问性 primitives |
| Component layer | `shadcn-svelte@1.1.1` | 业务组件样式和交互组合 |
| Design language | `Radix / shadcn` 风格约定 | Trigger/Content/Portal、可访问性、表单与 overlay 语义 |
| Styling | `tailwindcss@4.2.1` | 设计 token 与页面样式 |
| Charts | `echarts@6.0.0` | 监控与统计图表 |
| Language | `typescript@5.9.3` | 类型安全 |
| Adapter | `@sveltejs/adapter-node@5.5.4` | Node 部署 |
| Build | `vite@8.0.0` | 构建 |
| Svelte plugin | `@sveltejs/vite-plugin-svelte@7.0.0` | Svelte/Vite 集成 |

说明：

- `Bits UI` 是 Svelte 侧的 primitive 基础层
- `shadcn-svelte` 负责可复用业务组件和视觉骨架
- 不直接引入 React 版 `Radix UI`
- 若 `Bits UI` 未覆盖某个必要 primitive，优先本地补一层 Svelte 组件，并保持与现有 `Bits UI + shadcn-svelte` 外观和行为一致
- 后续新增依赖也必须固定版本，禁止 `latest`

## 3.2 数据获取原则

- 读密集、高聚合页面优先走 GraphQL
- 运维动作、会话登录、任务重试、健康检查优先走 Admin REST
- 首屏数据优先在 `+page.ts` / 组件内直接请求目标 MemStation 端点
- 客户端只负责：
  - 筛选条件同步
  - 局部刷新
  - 表单交互态
  - 图表渲染
  - 非阻塞 toast / dialog / drawer

## 3.3 鉴权原则

- manager 通过目标 MemStation 服务端 session 工作
- 登录页直接调用目标 MemStation 的 `/api/admin/login`
- 登出直接调用目标 MemStation 的 `/api/admin/logout`
- 浏览器端不保存 `MEMSTATION_ADMIN_TOKEN`
- 受保护页面统一在 server load 中校验会话

## 3.4 后端通信模式

manager 的所有业务接口都直接面向目标 MemStation：

- GraphQL：`<target>/graphql`
- Admin REST：`<target>/api/admin/*`
- Memory REST：`<target>/api/v1/memory/*`
- Gateway：`<target>/v1/chat/completions`、`<target>/v1/responses`、`<target>/v1/messages`
- Health：`<target>/health/live`、`<target>/health/ready`

这意味着：

- manager 不实现 API proxy
- manager 不复刻任何服务端业务逻辑
- manager 不维护独立数据库
- manager 只做 UI、状态组织、目标实例切换、请求编排和可视化

## 3.5 SvelteKit 服务端职责边界

`apps/manager` 中的 SvelteKit server 层只承担很薄的一层职责：

- 页面壳层渲染
- 静态资源服务
- 公开环境配置注入，例如默认 target、品牌信息、构建版本
- 非敏感路由守卫与跳转

明确不承担：

- GraphQL BFF
- REST proxy
- 会话代理
- 网关转发
- 审计写入
- 配置存储

如果未来必须有少量 server 端能力，也只能用于：

- 公开配置分发
- 非敏感 HTML 装配
- 与后端解耦无关的前端基础设施

## 3.6 页面实现原则

- 默认 SSR first
- 但所有业务数据源都直连目标 MemStation，而不是通过 manager 服务端转发
- 列表页必须有加载态、空态、错误态
- 危险操作必须二次确认
- 所有写操作界面都要显式提示会产生审计记录
- Playground 默认按“安全测试”模式工作，任何会话级记忆写入默认关闭

## 3.7 Chat UI 实现策略

Playground Chat 不依赖一套黑盒 chat kit 作为核心基础设施。

原因：

- 我们需要精确控制协议调试、thinking 展示、tool 调用、memory debug 和流式 markdown 行为
- 现成 chat kit 往往更偏终端聊天产品，而不是排障型管理控制台
- 风格需要完全统一到 `Bits UI + shadcn-svelte`

因此方案固定为：

- UI 骨架与 primitives 以 `Bits UI` 为主
- 业务组件使用 `shadcn-svelte` 组合与本地封装
- 若未来引入局部 chat 组件，只能作为内部子组件使用，不得主导整套视觉和数据模型

## 3.8 流式 Markdown 与 Thinking UI 要求

如果 Playground Chat 需要展示流式 markdown、thinking、tool 调用与 mixed content，则必须满足：

- 增量渲染正确，不因 chunk 边界破坏 markdown 结构
- 对长回复保持高效，避免每个字符都全量重解析整条消息
- 支持代码块、列表、表格、引用、链接、行内代码
- 支持 `thinking` / `reasoning` / `analysis` 类型消息的独立视觉容器
- thinking 默认折叠，可展开查看
- tool call / tool result 与正文严格区分
- markdown 渲染器需支持安全策略，禁止危险 HTML 注入

推荐实现方向：

- 消息内部先归一化为 block model
- 对 text/markdown block 做分块缓冲和节流刷新
- 使用专门的 markdown 渲染层，而不是直接把原始流拼进 `{@html}`
- thinking block 采用独立 `Collapsible` 容器，默认收起
- 长消息滚动区采用虚拟化或惰性折叠策略，避免页面持续膨胀

---

## 4. 设计风格与设计语言

## 4.1 总体方向

MemStation 管理面板的视觉方向固定为：

- 专业、明亮、信息密度高的 ToB 运维控制台
- 默认浅色主题，暗色仅作为补充
- 强调监控、排障、数据核查，而不是通用 AI 聊天审美
- 不采用紫色主色、不采用深色优先、不采用赛博霓虹风

建议的气质关键词：

- Ops cockpit
- Trustworthy
- Measured
- Dense but calm
- Data-first

## 4.2 版式语言

- 采用左侧主导航 + 顶部工作栏 + 主内容区的控制台布局
- 内容区不做过窄居中，而采用宽屏自适应信息面板布局
- 桌面端优先使用 12~14 列栅格
- 移动端缩减为单列堆叠，但仍保留关键状态卡片和可操作入口

推荐布局：

```text
+---------------------------------------------------------------+
| Top Bar: breadcrumb / env badge / refresh / search / account |
+----------+----------------------------------------------------+
| Sidebar  | Page header                                        |
|          +----------------------------------------------------+
|          | KPI strip / filters / content grid / tables        |
|          |                                                    |
|          | drawer / dialog / detail panel                     |
+----------+----------------------------------------------------+
```

## 4.3 字体与排版

- 主字体：`IBM Plex Sans` 或 `Geist`
- 等宽字体：`JetBrains Mono`
- 标题强调清晰层级，不做夸张大字 Hero
- 数字、ID、token、状态码、队列名、模型名全部使用等宽风格增强可读性

排版建议：

- 页面标题：28/32
- 分区标题：18/24
- 表格正文：13/20 或 14/20
- 辅助信息：12/16
- Monospace 元数据：12/18

## 4.4 色彩系统

推荐 token 方向：

- `--bg`: `#F4F7FB`
- `--surface`: `#FFFFFF`
- `--surface-muted`: `#EEF3F8`
- `--surface-elevated`: `#E7EDF5`
- `--border`: `#D6E0EA`
- `--text`: `#102033`
- `--text-muted`: `#5A6B7D`
- `--brand`: `#1E5AA8`
- `--brand-strong`: `#15467F`
- `--info`: `#2B6CB0`
- `--success`: `#2F855A`
- `--warning`: `#B7791F`
- `--danger`: `#C53030`

状态色规则：

- 成功：只用于通过、健康、已完成、写入成功
- 警告：用于 backlog、阈值逼近、待人工关注
- 错误：用于失败任务、不可用依赖、危险操作
- 蓝色：用于主流程、聚焦态、选中态、信息型图表

## 4.5 组件风格

- 卡片采用轻边框 + 低阴影，不做厚重投影
- 表格优先高密度、紧凑模式
- Badge 语义明确，避免花哨渐变
- 图表背景保持干净，减少装饰性网格
- Overlay 组件采用 `Dialog / Sheet / Drawer / Popover / DropdownMenu / Tooltip` 的 Radix/shadcn 式交互模型

## 4.6 动效规范

- 动效仅用于提升信息切换理解，不用于炫技
- 推荐时长：120ms ~ 180ms
- 使用淡入、轻微位移、折叠展开
- 禁止 bounce、过强 scale、连续闪烁

---

## 5. 信息架构与导航

## 5.1 一级导航

v1.0 固定 9 个一级模块：

1. Dashboard
2. Playground
3. Conversations
4. Memory
5. Users
6. Statistics
7. Settings
8. Operations
9. Audit

## 5.2 路由规划

推荐 `SvelteKit` 路由树：

```text
src/routes/
├── (auth)/
│   └── login/
├── (app)/
│   ├── +layout.server.ts
│   ├── +layout.svelte
│   ├── dashboard/
│   ├── playground/
│   │   ├── chat/
│   │   ├── memory/
│   │   └── protocols/
│   ├── conversations/
│   │   ├── +page.server.ts
│   │   └── [id]/
│   ├── memory/
│   │   ├── +page.server.ts
│   │   ├── summaries/
│   │   ├── impressions/
│   │   ├── facts/
│   │   └── keys/
│   ├── users/
│   │   ├── +page.server.ts
│   │   └── [id]/
│   ├── statistics/
│   │   └── tokens/
│   ├── settings/
│   │   ├── system/
│   │   ├── models/
│   │   └── providers/
│   ├── operations/
│   │   ├── health/
│   │   ├── queues/
│   │   ├── tasks/
│   │   └── cache/
│   └── audit/
```

说明：

- `/` 应重定向到 `/dashboard`
- `(auth)` 与 `(app)` 分组分离，避免布局耦合
- 登录页使用极简壳层
- 应用页共用统一 sidebar / header / toast / dialog 容器

## 5.3 顶部工作栏

顶部工作栏固定包含：

- breadcrumb
- target selector
- 当前环境标签，例如 `local` / `staging` / `prod`
- 全局搜索入口，后续可扩展为 command palette
- 手动刷新按钮
- 主题切换
- 管理员会话菜单

其中 `target selector` 是 manager 的核心能力之一，用于切换当前连接的 MemStation 目标。

---

## 6. 页面与模块详细功能

## 6.1 Login

路径：`/login`

功能：

- 输入 admin token
- 提交到服务端 action
- 成功后跳转 `/dashboard`
- 失败显示明确错误
- 已登录则直接重定向

设计要求：

- 独立、简洁、可信任
- 不做营销文案
- 使用单卡片登录框 + 系统标识 + 环境提示

## 6.2 Dashboard

路径：`/dashboard`

用途：单页概览系统当前工作状态和风险点。

必须显示：

- 活跃对话数
- 记忆总量
- Key Memory 数量
- 队列积压
- 最近失败任务数
- 最近审计事件数
- Token 使用趋势
- 最近异常或系统告警

推荐布局：

- 第一行：6 个 KPI 卡片
- 第二行：Token 趋势图 + Memory 增长图
- 第三行：队列状态表 + 最近审计/异常流

交互：

- 支持快速跳转到 Conversations / Operations / Audit
- KPI 卡片点击进入对应模块
- 图表时间范围切换：24h / 7d / 30d

数据来源建议：

- `GET /api/admin/overview`
- `Query tokenUsageStats`
- `GET /api/admin/queues`
- `GET /api/admin/audit`

## 6.3 Playground - Chat

路径：`/playground/chat`

用途：直接通过 MemStation gateway 与 bot 交流，验证代理、注入、记忆生成和工具链路。

核心能力：

- 发送聊天请求到目标 MemStation gateway
- 切换协议：
  - OpenAI Chat Completions
  - OpenAI Responses
  - Anthropic Messages
- 切换 mode：
  - transparent
  - inject
- 切换 provider override
- 设置 `X-MemStation-Session`
- 手动结束会话
- 切换 `Inject Context`、`Inject Tools`、`Tool Write`
- 切换 `Memory Persist`
- 显示流式输出
- 显示流式 markdown
- 显示 thinking / reasoning
- 显示 tool calls / tool results
- 显示 usage
- 显示本次请求关联的 conversation id / request metadata
- 显示本轮 memory debug 信息

布局建议：

- 左侧主区：聊天窗口
- 左下：输入区
- 右侧：记忆与调试面板

固定双栏布局：

- 左侧 7~8 列：ChatThread + Composer
- 右侧 4~5 列：Debug Panel

右侧 Debug Panel 需要分 4 个区块：

1. Request Controls
2. Injected Memory
3. Extracted Memory
4. Raw Debug

其中：

- `Injected Memory` 展示本轮请求前被召回和注入的记忆
- `Extracted Memory` 展示本轮对话后模型产出的 summary / impression / fact / key memory
- `Raw Debug` 展示 request headers、protocol、tool events、usage、conversation id

注意：

- `Extracted Memory` 可能是异步生成，因此需要展示 `pending / ready / failed`
- 如果目标后端未开启相应调试能力，右侧面板要给出 unsupported 提示

右侧控制面板包含：

- target backend
- protocol
- provider
- mode
- session id
- model
- memory injection 开关
- stream 开关
- memory persist 开关
- tool memory write 开关

默认值要求：

- `Memory Persist` 默认关闭
- `Tool Memory Write` 默认关闭
- `Inject Tools` 默认关闭
- `Inject Context` 默认开启与否由 playground preset 决定，但必须明确显示当前值

语义要求：

- `Memory Persist = false` 时，本次测试会话不应向 MemStation 写入 summary / impression / fact / key memory
- `Memory Persist = false` 时，即使 `Tool Memory Write = true`，也应以“禁止实际写入”为准
- manager 只负责发送该意图；真正的执行和兜底必须由 MemStation server 实现

说明：

- 不引入异构第三方 chat kit
- chat UI 由现成的 `Bits UI + shadcn-svelte` 组件组合而成，保证风格统一
- 消息项、输入区、调试侧栏由 manager 自己封装

消息渲染模型：

- 普通 assistant 消息：markdown bubble
- user 消息：plain / markdown bubble
- thinking：折叠的 reasoning panel
- tool call：结构化 call card
- tool result：结构化 result card
- usage：message footer
- debug events：右侧面板显示，不混入主线程正文

## 6.4 Playground - Memory

路径：`/playground/memory`

用途：验证“聊完 -> finalize -> summary/impression/fact/key memory 生成 -> 检索”的完整链路。

功能：

- 输入 session id / conversation id
- 查看该会话相关 summary / impression / fact / key memory
- 直接执行 memory search
- 对比 transparent 与 inject 差异
- 查看 recall tool 返回内容
- 展示排序分数和 effective score
- 对比 `Memory Persist on/off` 的差异
- 明确展示“本轮写入了哪些记忆、哪些没有落库”

## 6.5 Playground - Protocols

路径：`/playground/protocols`

用途：协议和网关调试页。

功能：

- 原始 request editor
- 原始 response viewer
- SSE event viewer
- header 配置
- 快速模板：
  - Chat Completions
  - Responses
  - Anthropic Messages

用途重点：

- 给管理员和开发者验证 gateway 兼容性
- 用于调试 provider 回写格式

## 6.6 Conversations - 列表页

路径：`/conversations`

用途：查找和筛选对话记录。

核心功能：

- 对话列表
- 关键字段展示
- 全文搜索或 ID 搜索
- 按 provider / mode / finalizeStatus / date range 筛选
- 快速跳转详情
- 对异常对话执行分析重试

表格建议字段：

- conversation id
- user id
- provider
- protocol
- mode
- finalize status
- summary status
- analysis status
- message count
- total tokens
- updated at

交互模式：

- 桌面端表格 + 右侧快捷筛选面板
- 行 hover 展示快捷动作
- 支持复制 conversation id
- 支持跳详情

## 6.7 Conversations - 详情页

路径：`/conversations/[id]`

用途：完整排查某条请求链路和后续记忆结果。

页面结构建议：

- 顶部：基础信息和状态 badge
- 主区左侧：message timeline
- 主区右侧：关联记忆、usage、metadata、操作区

必须显示：

- conversation 元信息
- protocol / provider / model
- finalize / summary / analysis 状态
- 消息时间线
- user / assistant / tool message
- tool calls 与 tool results
- usage 明细
- 关联 summary / impression / fact / key memory
- 原始 metadata / request id

支持动作：

- requeue conversation analysis
- 跳转到相关 memory 文档
- 复制 request / conversation id

UI 形式：

- message timeline 使用时间轴卡片
- tool call 采用可折叠 JSON 面板
- 右侧 detail aside 固定显示 status、usage 和关联对象

## 6.8 Memory - 工作台

路径：`/memory`

用途：统一记忆管理入口，包含搜索、浏览、修正和追溯。

页面目标：

- 一屏完成记忆检索
- 明确记忆类型区分
- 清楚展示来源对话与关系图
- 支持人工编辑与删除

顶部区域：

- 搜索框
- type filter
- score threshold
- date range
- include relations 切换

结果区域：

- 列表视图
- 详情 drawer
- 关系摘要面板

结果项至少显示：

- type
- content preview
- score
- effective score
- importance
- updated at
- source conversation

## 6.9 Memory - Summaries

路径：`/memory/summaries`

功能：

- 摘要列表
- topic label 筛选
- 查看来源 conversation
- 编辑 summary 文本
- 查看 summary 关联关键词和节点

## 6.10 Memory - Impressions

路径：`/memory/impressions`

功能：

- 印象列表
- description 预览
- confidence / metadata 查看
- 编辑 description
- 查看关联对话与关联事实

## 6.11 Memory - Facts

路径：`/memory/facts`

功能：

- 事实列表
- superseded chain 查看
- valid time 与 confidence 查看
- 编辑 fact 内容
- 查看关联 summary / impression / key memory

## 6.12 Memory - Key Memories

路径：`/memory/keys`

功能：

- Key Memory 列表
- 按 key / category / source / review status 筛选
- 新建
- 更新
- 删除
- 查看最近确认时间
- 查看审计记录

操作要求：

- 新建和编辑使用 `Dialog` 或 `Sheet`
- 删除必须二次确认
- 所有写操作旁边提示会记录审计事件

## 6.13 Users - 列表页

路径：`/users`

用途：查看系统内用户和其资源概况。

必须显示：

- user id
- name
- API key 数量
- 对话数
- Key Memory 数量
- 最近活跃时间

支持：

- 搜索
- 排序
- 进入用户详情

## 6.14 Users - 详情页

路径：`/users/[id]`

功能：

- 用户基础资料
- 用户设置
- API key 列表与 prefix
- token 使用概览
- 最近 conversations
- 最近 key memories

支持动作：

- 创建 API key
- 撤销 API key
- 更新用户 settings

## 6.15 Statistics - Tokens / Cost

路径：`/statistics/tokens`

用途：从运营视角查看 token 与成本变化。

功能：

- 时间范围选择
- group by day / week / month
- user filter
- provider filter
- token totals
- input / output tokens 趋势
- provider 占比
- top conversations by token
- estimated cost

说明：

- 若服务端暂时只返回 token 统计，则页面先展示 token 维度
- 成本图层可由模型价格表或系统配置补充

图表建议：

- 折线图：input / output / total
- 堆叠柱图：provider breakdown
- 排行表：top conversations / top users

## 6.16 Settings - System

路径：`/settings/system`

功能：

- 基础系统配置
- timeout / threshold / cache TTL / injection 参数
- 显示当前生效值
- 提交修改
- 修改前后 diff 提示

交互要求：

- 提交按钮显示保存中状态
- 成功后 toast
- 失败时展示字段级或全局错误

## 6.17 Settings - Models

路径：`/settings/models`

功能：

- chat model
- summary model
- analysis model
- embedding model
- tool write enabled
- max injection tokens
- 模型能力展示

展示形式：

- 左侧为系统默认配置
- 右侧为当前生效摘要
- 底部展示 provider / model capability reference

## 6.18 Settings - Providers

路径：`/settings/providers`

功能：

- OpenAI / Anthropic / Gemini provider 配置
- base URL
- API key 是否已配置
- 更新 provider credentials
- 隐藏敏感字段

要求：

- 只显示 `has_api_key`
- 不回显明文 key
- 明确提示写入会触发审计

## 6.19 Operations - Health

路径：`/operations/health`

用途：快速判断系统是否 ready，哪个依赖异常。

必须显示：

- live status
- ready status
- database
- redis
- vector store
- outbox relay
- worker / scheduler

展示形式：

- 状态矩阵
- 上次刷新时间
- 失败项详情 drawer

## 6.20 Operations - Queues

路径：`/operations/queues`

功能：

- 队列列表
- active / pending / scheduled / retry / archived / dead 数量
- 队列延迟和堆积风险提示
- 失败任务入口

交互：

- 点击队列进入任务面板或打开 drawer
- 对 backlog 高的队列给 warning badge

## 6.21 Operations - Tasks

路径：`/operations/tasks`

功能：

- 最近失败任务
- 根据 conversation id / task id 发起重试
- 查看任务 payload 摘要
- 查看错误原因

要求：

- 重试必须二次确认
- 重试完成后立即刷新队列计数

## 6.22 Operations - Cache

路径：`/operations/cache`

功能：

- L1 / L2 cache 状态摘要
- TTL 策略说明
- 命中率占位
- Redis Pub/Sub 失效广播状态

说明：

- v1.0 首先做状态可见性，不强求复杂图表

## 6.23 Audit

路径：`/audit`

用途：查看敏感动作与配置变更历史。

必须显示：

- actor type / actor id
- action
- target type / target id
- created at
- payload preview

支持筛选：

- action
- actor type
- target type
- date range

交互：

- 主表格 + 右侧 payload drawer
- payload 以 monospace JSON viewer 展示

---

## 7. 组件与前端模块拆分

## 7.1 推荐目录结构

```text
src/lib/
├── api/
│   ├── admin-rest.ts
│   ├── graphql.ts
│   ├── auth.ts
│   └── dto.ts
├── components/
│   ├── app-shell/
│   ├── navigation/
│   ├── data-table/
│   ├── cards/
│   ├── charts/
│   ├── forms/
│   ├── entities/
│   ├── feedback/
│   └── audit/
├── server/
│   ├── api.ts
│   ├── auth.ts
│   └── loaders/
├── stores/
│   ├── filters.ts
│   ├── theme.ts
│   └── refresh.ts
├── utils/
│   ├── format.ts
│   ├── dates.ts
│   ├── status.ts
│   └── chart-theme.ts
└── styles/
    ├── tokens.css
    └── utilities.css
```

## 7.2 基础组件层

优先复用或封装以下组件：

- AppShell
- SidebarNav
- TopBar
- PageHeader
- KPIStatCard
- StatusBadge
- FilterBar
- SearchInput
- EmptyState
- ErrorPanel
- ConfirmDialog
- JSONViewer
- EntityDrawer

## 7.3 数据展示组件

- DataTable
- Timeline
- KeyValueGrid
- MetricStrip
- QueueStatusGrid
- AuditEventTable
- UsageTrendChart
- ProviderShareChart
- MemoryRelationList

## 7.4 交互组件约束

- `Dialog`：用于危险确认、配置保存确认、API key 创建结果
- `Sheet` / `Drawer`：用于详情查看和轻量编辑
- `Popover`：用于轻筛选和次级说明
- `Tabs`：用于 memory 子域切换
- `DropdownMenu`：用于表格行快捷动作
- `Tooltip`：用于状态解释和字段提示
- `Command`：后续作为全局搜索和跳转入口

## 7.5 Playground / Chat UI 组件规划

为了保持风格统一，chat playground 不使用独立的陌生视觉组件库，而是基于现成组件组合：

- `ScrollArea`：消息滚动区
- `Textarea`：输入框
- `Button`：发送、停止、重试
- `Tabs`：协议切换
- `Select`：provider / model / target 选择
- `Switch`：stream / inject / tools 开关
- `Tooltip`：字段提示
- `Collapsible`：tool calls / tool results 折叠区
- `Sheet`：调试详情侧栏
- `Badge`：message role / status / provider 标签
- `DropdownMenu`：消息快捷动作
- `Separator`：线程分区

在这些基础上封装：

- `ChatThread`
- `ChatComposer`
- `ChatMessageCard`
- `ThinkingPanel`
- `ToolCallCard`
- `ToolResultCard`
- `UsageFooter`
- `GatewayRequestPanel`
- `MemoryInjectionPanel`
- `MemoryExtractionPanel`
- `SessionWriteGuard`

---

## 8. 数据映射与接口使用

## 8.1 GraphQL 负责的页面

- `/dashboard`
- `/conversations`
- `/conversations/[id]`
- `/memory`
- `/memory/summaries`
- `/memory/impressions`
- `/memory/facts`
- `/memory/keys`
- `/users`
- `/users/[id]`
- `/statistics/tokens`

适合原因：

- 字段密集
- 聚合读取多
- 需要多个卡片或多个实体并排展示

## 8.2 REST 负责的页面或动作

- `/login`
- `/playground/chat`
- `/playground/protocols`
- `/operations/health`
- `/operations/queues`
- `/operations/tasks`
- `/settings/providers`
- `/settings/system`
- `/settings/models`
- `requeue`
- `logout`

## 8.3 页面到接口映射

| 页面 | 首选接口 |
|------|----------|
| Dashboard | `GET /api/admin/overview` + `Query tokenUsageStats` |
| Playground Chat | Gateway APIs + memory debug endpoints |
| Playground Memory | Gateway APIs + `POST /api/v1/memory/search` + GraphQL + conversation detail |
| Playground Protocols | Gateway APIs |
| Conversations | `Query conversations` |
| Conversation Detail | `Query conversations` 的 detail 扩展 |
| Memory Search | `Query searchMemories` 或 `POST /api/v1/memory/search` |
| Key Memories | `Query keyMemories` + `PUT/DELETE /api/v1/memory/keys/:key` |
| Users | `Query users` / `Query me` |
| Token Stats | `GET /api/admin/stats/tokens` 或 `Query tokenUsageStats` |
| Config | `GET /api/admin/config` / `PUT /api/admin/config` |
| Health | `/health/live` + `/health/ready` |
| Queues | `GET /api/admin/queues` |
| Requeue | `POST /api/admin/tasks/requeue` |
| Audit | `GET /api/admin/audit` |

---

## 9. 交互规范

## 9.1 列表页

- 默认使用 server pagination
- 默认支持 URL 查询参数同步
- 筛选变更后更新 URL，便于分享和回放
- 不使用无限滚动作为主交互

## 9.2 详情查看

- 列表中的 entity 点击后进入详情页
- 轻量详情使用 drawer
- 深度排障使用完整详情页

## 9.3 写操作

- 所有写操作都要：
  - 显示确认
  - 说明影响范围
  - 提示会记录审计日志
  - 成功后刷新局部数据

## 9.4 反馈机制

- 成功：toast + 局部刷新
- 失败：内联错误 + toast
- 重要失败：保留详细错误信息 drawer

## 9.5 空态和错误态

- 空态必须给出下一步行动
- 错误态必须给出刷新或返回动作
- 图表空态不能只显示空白区域

---

## 10. 响应式与设备适配

桌面端优先，但必须保证移动端可用。

桌面端：

- 左侧固定导航
- 详情页可双栏或三栏
- 大表格支持横向滚动

平板端：

- sidebar 可折叠为 icon rail
- filter 面板改为 drawer

移动端：

- 顶部汉堡导航
- KPI 卡片 1 列堆叠
- 表格切换为 card list
- 详情区改为纵向堆叠

---

## 11. 多目标实例与集群化设计

## 11.1 Target 抽象

manager 必须以 `Target` 作为一等概念，而不是把某个固定 base URL 写死。

一个 target 至少包含：

- `id`
- `label`
- `base_url`
- `environment`
- `cluster_name`
- `region`
- `kind`：`single` / `cluster-entry` / `node`
- `graphql_endpoint`
- `admin_rest_base`
- `gateway_base`

## 11.2 Target 选择与存储

target 选择策略：

- 当前激活 target 保存在 URL 或前端本地状态
- 可选地保存在浏览器本地 profile 中
- 默认不存储任何明文管理密钥

## 11.3 Cluster 兼容方向

后续 cluster 化时，manager 需要支持三类目标：

1. Cluster 入口
2. Region 入口
3. Node 级诊断入口

UI 层面要预留：

- target selector
- scope badge
- cluster / region / node 筛选
- 节点级健康页和队列页扩展位

## 11.4 直接连接架构下的服务端要求

既然 manager 直接连接目标 MemStation，而不是通过 SvelteKit proxy，那么 MemStation server 必须自己承担：

- CORS allowlist
- `credentials: include` 所需跨域配置
- 管理 session cookie 策略
- GraphQL / REST / gateway 统一认证
- 集群入口和节点入口的 API 暴露
- Playground 所需的 memory debug 与 memory persist 控制能力

特别说明：

- 如果 manager 与目标 MemStation 不同源部署，MemStation 的 admin session 需要支持显式跨站管理模式
- 该模式应由 MemStation server 配置控制，而不是由 manager 绕过
- 如果要支持“测试会话默认不写记忆”，该约束必须由 MemStation server 在 gateway 层落实，而不是由 manager 仅靠 UI 约定

---

## 12. 前端实施顺序

## Phase 1 - App Shell 与鉴权

- 登录页
- 受保护 layout
- sidebar / top bar
- 主题 token
- target selector
- API client 骨架

## Phase 2 - Playground / Dashboard / Health / Queues / Audit

- 先打通 direct-to-MemStation 主链路
- playground 先落地，确保 gateway / memory 链路能被 UI 直接验证
- 再补系统观测主链路

## Phase 3 - Conversations / Conversation Detail

- 把请求链路和工具链路先可视化
- 打通从 dashboard 到 conversation 的追踪路径

## Phase 4 - Memory Workspace

- Memory Search
- Summaries / Impressions / Facts / Keys
- 编辑和删除动作

## Phase 5 - Users / Statistics / Settings

- 用户与 token 运营能力
- 配置管理和 provider 配置

## Phase 6 - 细节打磨

- 空态、错误态、骨架屏
- 动效和密度优化
- 暗色主题补充
- E2E 测试

---

## 13. 项目职责边界

## 13.1 `apps/server`

`apps/server` 是唯一的业务核心，负责：

- gateway
- memory system
- admin REST
- GraphQL
- auth / session
- queue / worker
- audit
- cluster-ready control APIs

manager 所有真正的业务读写都来自这里。

## 13.2 `apps/manager`

`apps/manager` 是独立部署的管理控制台，负责：

- 信息架构
- 视觉系统
- 页面交互
- 目标实例切换
- 直接请求目标 MemStation
- 管理与调试体验

不负责：

- 代理 API
- GraphQL BFF
- 数据持久化
- 业务规则计算
- 会话签发

## 13.3 SvelteKit server 层

SvelteKit server 层只负责：

- HTML 壳层
- 静态资源
- 非敏感运行时配置
- 少量前端路由守卫

明确不负责：

- 转发请求到 MemStation
- 缓存业务数据
- 代替 MemStation 做登录态管理
- 对业务数据做二次封装

---

## 14. 设计验收标准

管理面板达到 v1.0 可交付时，应满足：

1. 登录、登出、会话保护稳定可用
2. Dashboard 能让管理员在 30 秒内判断系统是否异常
3. Playground 能通过 gateway 验证代理、注入、记忆链路
4. Conversations 和 Memory 页面能支撑完整排障路径
5. Key Memory、Fact、Impression、Summary 支持人工修正
6. Settings、Queues、Audit 可以完成常见运维动作
7. 页面在桌面和移动端均可访问
8. manager 可以切换并连接任意指定的 MemStation target
9. 视觉风格符合 `AGENTS.md` 要求，而不是通用 AI 深色紫色控制台

---

## 15. 对现阶段实现的直接影响

接下来 `apps/manager` 的实现应以本文件为准，优先落以下骨架：

- 应用级 layout
- target selector
- 登录页
- Playground Chat
- Dashboard
- Operations: Health / Queues
- Conversations 列表与详情
- Memory 工作台

也就是说，manager 不再只是“最小壳层”，而是进入正式的 v1.0 管理面板实施阶段，并且从第一天起就是一个可连接任意 MemStation 后端的独立控制台。
