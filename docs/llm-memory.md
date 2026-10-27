# LLM 记忆生成

语义记忆仅通过配置的 LLM 生成。已删除关键词匹配、固定画像、文本拼接摘要、模板事实和启发式重要性评分的生产入口。在线 Worker、generation rebuild 和 Playground 预览复用同一处理服务。

## 模型职责与配置

| 阶段 | 配置角色 | 输出 |
| --- | --- | --- |
| 摘要 | `SUMMARY_MODEL` / 用户有效 `summary_model` | 摘要、主题、关键词、重要性、衰减系数 |
| 分析 | `ANALYSIS_MODEL` / 用户有效 `analysis_model` | Fact、可选 Impression、Task/Event/Procedure/Entity/Agent Action/Artifact、语义关系 |
| Key Memory | `analysis_model` | 根据已验证事实和现有 Key Memory 合并长期核心信息 |
| 来源片段和人工 Key 的评分 | `summary_model` / `analysis_model` | 重要性、衰减系数、活动时间及理由 |
| 向量 | 部署级 `EMBEDDING_MODEL` | Embedding，不参与语义事实生成 |

模型名保持配置化。支持 provider 前缀；没有前缀时使用内部默认 provider。不存在隐式切换模型或规则兜底。

自动 Key Memory 白名单使用 `MEMSTATION_KEY_MEMORY_AUTO_KEYS`，默认 `identity,preferences,projects`。它与受用户开关保护的工具写入白名单 `MEMSTATION_KEY_MEMORY_WRITE_KEYS` 分开；不会再自动把会话 ID 写成 `recent_conversation`。空的自动白名单关闭自动合并，人工管理和工具写入仍按各自权限执行。

## 处理与校验

1. 会话结束、outbox 投递和幂等任务链路保持原有结构。
2. LLM 读取带角色和来源 ID 的原始消息、结构化内容与已完成的提取产物，生成摘要；长文本经过 UTF-8 安全分块和分层 LLM 合并。
3. LLM 分析原始来源，区分否定、纠正、假设、计划、已完成事件和说话人归属；摘要及旧事实仅作为上下文。
4. 服务端严格校验 JSON schema、有限分数范围、数量、长度、原文引用和作用域。每条派生记忆必须附真实来源的精确引用；助手建议不能作为用户事实证据。精确引用存在不代表模型释义必然正确，人工修正仍然必要。
5. 事实替代必须明确引用同属主、相同语义键和 subject 的旧事实。没有明确替代指令时不按相似文本猜测覆盖。
6. Key Memory 仅引用本次已经验证的 Fact，从允许键中选择更新；没有值得记忆的信息可以返回空结果，不强行构造画像。

保留的确定性处理负责权限、来源序列化、分块、稳定 ID、引用验证、图结构完整性、时间衰减计算、排序和 Token 装包。这些处理不承担语义事实生成。更大的 `decay_factor` 表示遗忘更慢。

## 失败与重试

- 未配置模型、超时、上游错误、非法 JSON、虚构来源、越权键和不合法分数都会返回错误，异步任务进入既有重试机制；已保存的原始对话不会被规则结果替代。
- 分析允许 `facts: []`、`items: []`、`impression: null`，不会写入空壳记忆或为空 ID 创建向量任务。
- 摘要／分析服务遇到严格 JSON schema 错误时，最多再调用一次配置的 LLM，从相同原始来源重新生成完整对象；不把拒绝的候选当作新证据，不把模型控制的错误字段名提升为指令。每次调用仍受原有预算限制，超时和传输错误不在这一层重试。第二次仍不合法则交回任务重试；引用、分数及事实替代校验不放宽，也不做字符串到数组的语义补救。解析先写入全新的临时对象，成功后才替换结果，避免部分结果或旧 map 残留。
- 摘要和分析在派生写入前持久化已验证结果；同一来源的并发处理保留首个持久化结果，后续任务读取同一版本，存储重试复用结果，避免重复调用产生不同事实。检查点是存储内部字段，不通过记忆 JSON 暴露。
- Key Memory 跳过已被替代的事实，并按允许键读取现有上下文；在摘要上保存候选与原始写入版本，重复任务复用候选；条件写入拒绝覆盖期间的人工或其他任务修改。冲突会保留当前内容并报告任务失败，需要检查后再处理，不能静默覆盖。
- 已经人工改写的文档通过节点版本阻止 LLM 原代次重放覆盖；冲突会失败并保留当前正文和向量。需要重新生成时使用隔离 generation 检查并显式激活。
- 原始片段仍是可检索来源，模型失败不表示已有的来源或已验证摘要必须被回滚。

每次模型调用有超时、输入和输出预算：输入至多 128 KiB，输出 JSON 至多 128 KiB；摘要来源每批至多 16,000 bytes、单来源片段至多 12,000 bytes、至多 32 批，合并每组至多 4 份摘要。旧事实候选上下文按完整记录限制在 32 KiB，并明确告知模型是否有候选未提供；替代引用只允许指向已提供的活动事实。分块输出的关系键只在该分块内解析，不会误连其他分块。输出 Token 预算为评分 2,048、分析 8,192、其余 4,096。超过预算会失败，不静默丢弃尾部内容。当前画像采用首个有证据的分块观察，长会话的跨块画像合并仍需进一步评测。

## 历史数据与兼容

升级前执行版本化迁移 `202610240005_llm_summary_metadata.sql` 、`202610240006_llm_impression_metadata.sql` 、`202610240007_llm_fact_metadata.sql` 和 `202610250001_llm_fact_history_metadata.sql`，为 Summary、Impression 和 Fact 增加 JSONB 元数据，持久化检查点、LLM 来源和原文引用。历史快照触发器同时展开来源元数据，并修正已有嵌套快照，不改变历史内容或时间。检查点通过专用 store 方法合并写入，不重置向量 ready 状态或人工编辑版本。既有规则生成的历史记忆保留；升级不会自动删数据、批量调用模型或替换当前 generation。需要更新旧记忆时，使用现有 generation rebuild，在隔离代次检查后显式激活。新的在线提取和重建均使用 LLM。

Workspace、Actor、Agent 与受众权限遵循 [独立记忆服务契约](memory-service-contract.md)；抽取结果不能扩大发言者或调用者的授权范围。

## 实验性语义事件与推理设置

应用迁移 `202610250015_semantic_episodes.sql` 后，可通过
`MEMSTATION_SEMANTIC_EPISODES_ENABLED=true` 启用后续 episode 抽取，默认关闭。
server 和所有 worker 应保持一致配置。关闭开关不删除已保存的 episode，
历史记忆不会自动重新抽取；检索仍可读取已经持久化的类型。

事件叙述与 Fact、summary、task 共存。状态、人物、时间原话和逐字引用由
LLM 提供并受来源校验；来源会话日期单独保存，不用处理时间代替事件时间。
Manager 的记忆类型筛选和详情支持 Semantic episodes。

`MEMSTATION_MEMORY_REASONING_EFFORT` 可显式设置内部摘要、分析和评分的推理强度，
默认空值沿用 provider 默认。当前仅适用于支持该参数的 OpenAI-compatible
Chat / Responses；不改变被代理的用户请求。先核对模型支持值，再在专用环境测试。

启用 episode 时，分析每批最多 12 段来源、6,000 字节，最多 32 批；
完整验证的批次按输入、策略和推理设置持久化，后批失败重试可复用先前结果。
批次 checkpoint 总预算为 256 KiB，不对外返回内部元数据。

原始文本索引使用有界 UTF-8 切片并保留来源字节区间。旧来源索引不会因为
升级或普通保留型重建而自动重新切分，需要另行规划历史迁移。

## 评测

使用 [手动评测入口](../apps/server/tests/eval/README.md) 检查真实模型的来源覆盖、
回答、失败率和耗时。确定性 fixture 用于契约与可靠性验证，不代表真实模型质量。
每轮报告、失败回复和程序化输出保存在本地忽略目录，不放入产品文档。

## Output budgets and worker deadlines

Memory processing defaults to an initial output budget of 16,384 tokens and a deployment ceiling of 65,536. Configure `MEMSTATION_MEMORY_MIN_OUTPUT_TOKENS`, `MEMSTATION_MEMORY_MAX_OUTPUT_TOKENS` and `MEMSTATION_MEMORY_LLM_TIMEOUT_SECONDS` (default 300 seconds). The initial request uses the larger of the task's explicit budget and the configured minimum. A request above the configured maximum is rejected before inference; it is never silently reduced. Limits must satisfy `1 <= minimum <= maximum <= 262144`. Configure a ceiling supported by the actual upstream model.

Only an explicit length/max-tokens finish reason permits one retry of the same input at the configured ceiling. Both attempts share the deadline and their token usage is added. Partial output is never accepted as a complete memory. Schema/source-grounding correction remains separately bounded and never receives rejected output as evidence. Reasoning effort and output limits are part of the analysis checkpoint identity. Internal model adapters use at least the memory deadline, so the gateway's shorter provider timeout does not silently cap a worker call. Broker-backed summary/analysis calls use this generation deadline too; broker embedding and rerank calls retain the shorter provider timeout.

Manual failure-cohort evaluation is documented in [the evaluation guide](../apps/server/tests/eval/REGRESSION.md). Dataset files, selections, model outputs and reports remain under ignored `.agents/archive/`; the failure-enriched cohort is a development diagnostic and is not a representative benchmark score.

Native images, audio and other attachments follow the selected model and transport capability policy. Supported media is attached to Summary/Fact requests; unsupported media becomes a text marker and cannot support a fact. See [Native media input](multimodal-input.md).
