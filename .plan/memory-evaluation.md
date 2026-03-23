# MemStation — 记忆评测与召回准确率测试计划

## 1. 目标

本文件定义 MemStation v1.0 的记忆评测方案，覆盖：

1. 记忆抽取是否正确
2. 记忆召回是否准确
3. 排序是否把“该出现的记忆”放到前面
4. 注入后的回答是否更好、且不引入错误记忆
5. 多用户隔离、事实替代、时间衰减、图扩展是否按设计工作

注意：

- 这里的评测是产品级评测，不只是单元测试
- 评测必须能在本地、CI、夜间回归三种模式下运行
- 外部 CLI 不新增命令，评测通过 `apps/server/tests/eval` 和 `scripts/` 驱动
- v1.0 优先做 deterministic offline eval + fixture e2e，真实模型 eval 放夜间任务

---

## 2. 参考基线

我们参考 Mem0 的公开做法，但不照搬其产品假设。

当前可直接借鉴的两点：

1. Mem0 在仓库里直接维护 benchmark scripts，并明确跑 `LOCOMO` 与 `OpenAI-MRCR` 这类长程记忆 / 多轮引用任务，而不是只做主观 demo。
2. Mem0 在检索文档里把“语义匹配、时间因素、使用频率、标签匹配”等信号拆成可加权 criteria，再汇总成最终分数，而不是只盯 embedding cosine。

MemStation 的实现比 Mem0 多了：

- `MemorySummary / Impression / Fact / KeyMemory` 分层
- `MemoryNode + MemoryRelation + MemoryEmbedding` 三层结构
- `semantic_score * importance * time_decay` 排序
- 显式 outbox / worker 链路
- 管理面板可见的 recall debug 和 conversation-linked memory trace

所以我们的评测必须同时覆盖：抽取正确性、召回正确性、排序正确性、注入有效性、异步链路稳定性。

---

## 3. 评测原则

### 3.1 先做离线真值，再做在线体验

优先级固定为：

1. 离线 golden dataset
2. 组件级 deterministic eval
3. 端到端 fixture eval
4. 夜间真实模型 eval
5. 线上 shadow / 人工 spot check

### 3.2 先测 recall，再测 answer quality

如果召回本身不准，回答质量评测会被污染。

因此必须拆成：

- Extraction Eval
- Retrieval Eval
- Ranking Eval
- Injection Eval
- End-to-End Conversation Eval

### 3.3 先做可重复，再做“更智能”

CI 中默认不依赖外部商用模型稳定输出。

CI 里优先使用：

- 固定 deterministic benchmark embedder
- fixture provider adapter
- 固定 JSON 输出样本
- 固定 conversation transcript

真实模型评测只放到：

- 夜间定时任务
- 发版前手工回归
- 模型升级前后对比

### 3.4 错误记忆比漏掉记忆更危险

对于个人记忆系统，误召回、串用户、召回已过期事实，通常比“少召回一条”更危险。

因此必须单独跟踪：

- Wrong User Leakage
- Stale Fact Hit Rate
- Contradicted Fact Top-K Rate
- Hallucinated Memory Citation Rate

---

## 4. 评测维度

| 维度 | 目标 | 代表指标 |
|------|------|----------|
| 抽取正确性 | 对话结束后生成正确的 summary / fact / impression / key memory | precision, recall, F1, schema validity |
| 召回准确率 | query 能召回正确 memory | Recall@K, Hit@K, MRR |
| 排序质量 | 正确 memory 排在前面 | nDCG@K, Precision@K, Top1 accuracy |
| 新旧事实处理 | superseded / stale memory 不应错误前置 | stale top3 rate, contradiction leakage |
| 图扩展质量 | 1-2 hop relation 补全有用而不失控 | relation expansion gain, noise rate |
| 注入有效性 | 注入后回答更好 | grounded answer rate, answer F1, preference carryover |
| 安全隔离 | 不串用户、不越权、不误写 key | wrong-user leakage, unauthorized write rate |
| 性能稳定性 | recall 和 injection 不破坏延迟目标 | p50/p95 latency, TTFT overhead |

---

## 5. 数据集设计

## 5.1 数据源分层

评测数据分为四层：

1. `synthetic`: 人工可控、用于边界条件
2. `golden`: 人工标注、用于主回归
3. `fixture`: 直接驱动 E2E 和 manager playground
4. `shadow`: 真实使用脱敏样本，发版前抽样复验

## 5.2 每条样本必须包含的字段

建议统一为 JSONL 或 YAML，字段至少包括：

```yaml
case_id: retrieval.pref.stable.001
user_id: user-alpha
history:
  - conversation_id: conv-001
    messages: []
expected_memories:
  positives:
    - memory_id: summary-1
      type: summary
      min_rank: 3
    - memory_id: key-1
      type: key_memory
      min_rank: 1
  negatives:
    - memory_id: fact-old-1
      reason: superseded
query:
  text: "我平时更喜欢哪种代码风格？"
filters:
  memory_types: [summary, key_memory, fact, impression]
scoring_expectation:
  stale_must_not_be_top_k: 3
  wrong_user_allowed: false
answer_expectation:
  must_include:
    - "函数式"
  must_not_include:
    - "Java 是最喜欢的语言"
```

## 5.3 必须覆盖的场景

### A. 稳定偏好

- 常用语言
- 回复风格偏好
- 长期工具偏好
- 设备偏好
- 地理/时区

### B. 会变更的事实

- 城市迁移
- 工作角色变化
- 项目阶段变化
- 计划变更
- 家庭/关系事实更新

### C. 易混淆场景

- 同主题多轮对话
- 否定与修正
- “以前是 A，现在是 B”
- 相似但不同的人/项目/地点
- 不同用户拥有相似事实

### D. 图扩展场景

- Summary -> Fact
- Fact -> Key Memory
- Impression -> Summary
- supersedes 关系链

### E. 注入与安全

- tool write disabled
- tool write enabled but key 不在白名单
- transparent mode 不注入
- inject mode 注入但不持久化
- memory persist off 的 playground 验证

### F. 多语言

- 中文查询召回中文记忆
- 英文查询召回英文记忆
- 中英混合 query 召回正确跨语记忆

---

## 6. 评测套件设计

## 6.1 Extraction Eval

目标：验证 conversation finalize 后的异步链路输出是否正确。

检查对象：

- `MemorySummary`
- `MemoryKeyword`
- `Impression`
- `Fact`
- `KeyMemory`
- `MemoryRelation`
- `OutboxEvent` -> task state transitions

指标：

- JSON schema validity = 100%
- Summary topic split accuracy
- Fact precision / recall / F1
- KeyMemory whitelist compliance = 100%
- Impression precision / recall
- supersedes chain correctness
- duplicate suppression rate

建议首批 gate：

- schema validity = 100%
- key memory 非白名单写入 = 0
- fact F1 >= 0.85
- key memory precision >= 0.95
- supersedes chain correctness = 100%

## 6.2 Retrieval Eval

目标：验证 `/api/v1/memory/search`、admin memory search、GraphQL `searchMemories` 的召回质量。

指标：

- Recall@1 / @3 / @5 / @10
- Precision@3 / @5
- Hit@1 / @3 / @5
- MRR
- nDCG@5 / @10
- query embedding token estimate consistency
- wrong-user leakage = 0
- stale fact top3 rate

建议按 memory type 分桶：

- `key_memory`
- `summary`
- `fact`
- `impression`
- mixed

建议首批 gate：

- stable preference / key memory: Recall@5 >= 0.95
- stable factual memory: Recall@5 >= 0.90
- cross-conversation summary recall: Recall@5 >= 0.85
- impression recall: Recall@5 >= 0.80
- wrong-user leakage = 0
- superseded fact 出现在 Top3 的比例 <= 0.02

## 6.3 Ranking Eval

目标：验证 `semantic_score * importance * time_decay` 和 1-2 hop relation expansion 是否真的改善排序。

必须做 ablation：

1. 仅 `semantic_score`
2. `semantic_score * importance`
3. `semantic_score * importance * time_decay`
4. 完整排序 + relation expansion

每个 query 输出：

- candidate ids
- semantic_score
- importance
- time_decay
- effective_score
- relation-expanded from which node

重点观测：

- Top1 是否从 stale fact 切换到最新 fact
- relation expansion 是否把强相关 fact 拉进 TopK
- relation expansion 是否引入噪音 node
- importance 是否让 Key Memory / 高价值 summary 稳定前排

建议首批 gate：

- 完整排序相较纯 semantic，nDCG@5 至少提升 8%
- relation expansion 在目标集上的 Recall@5 至少提升 5%
- relation noise rate <= 10%

## 6.4 Injection Eval

目标：验证 recall + injection 是否真正帮助回答，而不是只让上下文更长。

方法：

- 同一问题跑两次：`transparent` vs `inject`
- 固定 provider fixture 或固定模型版本
- 使用 gold answer / must-include / must-not-include 规则评分

指标：

- grounded answer rate
- answer exact match / F1
- memory usage correctness
- preference carryover success rate
- stale citation rate
- tool call usefulness rate

建议首批 gate：

- inject 相比 transparent，answer F1 提升 >= 10%
- grounded answer rate >= 0.90
- stale citation rate <= 0.03
- wrong memory citation rate = 0

## 6.5 End-to-End Conversation Eval

目标：验证从 gateway 到 memory plane 到 manager debug 的整链路。

固定流程：

1. 发起一轮或多轮对话
2. 显式结束或触发 finalize
3. 等待 summary / analysis / key memory worker 完成
4. 用新 query 做 recall
5. 检查 manager playground 是否能看到一致的 debug 输出

这里除了结果正确，还要检查：

- Conversation 状态流转
- Outbox 幂等
- Worker retry 不重复写脏数据
- AuditEvent 是否完整
- manager 中 debug panel 与后端结果是否一致

---

## 7. 仓库落位方案

评测代码不走新的公开 CLI，直接落在测试与脚本层：

```text
apps/server/tests/eval/
  extraction/
  retrieval/
  ranking/
  injection/
  fixtures/
  datasets/
  golden/

scripts/
  eval-memory.sh
  eval-memory-nightly.sh
  export-shadow-samples.sh
```

其中：

- `tests/eval/extraction` 负责 worker 输出正确性
- `tests/eval/retrieval` 负责 TopK 召回
- `tests/eval/ranking` 负责打分分解和 ablation
- `tests/eval/injection` 负责 inject vs transparent 对比
- `fixtures/` 放 deterministic provider / embedder 输入输出
- `golden/` 放人工标注基线

manager 侧不直接承担评测逻辑，但要保留可验证 UI：

- Playground Chat 的 thinking / extracted / diff / raw debug
- Playground Memory 的 conversation inspect + GraphQL search
- Memory Workspace 的 semantic search 和 detail open

---

## 8. 指标定义

## 8.1 Retrieval 核心指标

### Recall@K

gold 正例中，有多少至少出现在 TopK。

### Precision@K

TopK 结果中，有多少是 gold 正例。

### MRR

首个正确结果越靠前越好。

### nDCG@K

考虑 rank 权重，适合多条相关记忆同时存在的情况。

### Wrong User Leakage

TopK 中是否出现其它 `user_id` 的任何 node。

要求：始终为 0。

### Stale Fact TopK Rate

已被 supersede 的旧事实进入 TopK 的比例。

### Relation Expansion Gain

有无 relation expansion 时，Recall@K 的提升幅度。

## 8.2 Extraction 核心指标

### Fact F1

对 gold fact 集做实体级 / 断言级对齐。

### KeyMemory Precision

进入 key slot 的内容是否真的满足白名单和值域约束。

### Summary Topic Accuracy

多主题对话是否拆成合理 summary cluster。

### Audit Completeness

自动写入、人工改写、tool write 是否都有审计记录。

## 8.3 Injection 核心指标

### Grounded Answer Rate

回答中的关键结论能否在检索结果或用户当前消息中找到依据。

### Preference Carryover Success

下一轮回答是否正确沿用稳定偏好。

### Contradiction Leakage Rate

回答是否引用了已被替代或已被否定的旧记忆。

---

## 9. 评分与判定方式

## 9.1 先规则判定，后 LLM-as-judge

v1.0 默认顺序：

1. rule-based exact / set / regex matching
2. structured diff scoring
3. 必要时再增加 LLM judge

原因：

- rule-based 更稳定，适合 CI
- LLM judge 易漂移，更适合夜间或人工复核

## 9.2 LLM judge 只做补充项

LLM judge 可用于：

- summary 是否忠实原对话
- impression 是否过度推断
- 注入后的回答是否“自然且有帮助”

但不能用于：

- 多用户隔离判定
- 白名单合规判定
- superseded fact 判定
- 审计完整性判定

这些必须是硬规则。

---

## 10. 与 Mem0 的对齐方式

## 10.1 借鉴点

借鉴 Mem0 的两种思路：

1. 公开 benchmark 脚本，持续跑 long-context / memory benchmark
2. 检索时把多种 criteria 显式打分，而不是只看向量相似度

## 10.2 我们的本地化改造

MemStation 不直接照抄 Mem0 的分数模型，而是映射到自己的排序结构：

- `semantic_score` 对应语义匹配
- `importance` 对应长期价值 / 业务优先级
- `time_decay` 对应新鲜度
- relation expansion 对应图结构补全
- `review_status / source / supersedes` 参与 precision 约束

因此 MemStation 的 recall eval 除了 `Hit@K`，还必须输出：

- candidate source
- relation source
- stale / superseded flags
- review status
- effective_score 分解

---

## 11. CI、夜间和人工回归分工

## 11.1 CI 必跑

- unit tests
- integration tests
- deterministic retrieval eval small set
- extraction schema / whitelist / audit hard rules
- manager fixture e2e

要求：

- 5 分钟内可完成
- 不依赖外部真实模型

## 11.2 Nightly 必跑

- full golden retrieval suite
- inject vs transparent answer eval
- provider contract regression with fixed model version
- latency / worker backlog smoke eval

## 11.3 发版前人工回归

由 operator 在 manager 中走一遍：

1. Playground Chat preview mode
2. Playground Chat persist mode
3. Playground Memory inspect
4. Memory Workspace semantic search
5. Key Memory CRUD
6. conversation detail -> source memory trace

---

## 12. v1.0 首批门禁建议

| 类别 | 建议门禁 |
|------|----------|
| Wrong user leakage | `0` |
| Key memory whitelist violation | `0` |
| Extraction schema validity | `100%` |
| Stable preference Recall@5 | `>= 0.95` |
| Stable fact Recall@5 | `>= 0.90` |
| Cross-conversation summary Recall@5 | `>= 0.85` |
| Impression Recall@5 | `>= 0.80` |
| Superseded fact Top3 rate | `<= 0.02` |
| Inject vs transparent answer F1 uplift | `>= 10%` |
| Grounded answer rate | `>= 0.90` |
| Memory search P95 | `< 250ms` |
| Summary pipeline P95 | `< 60s` |
| Analysis pipeline P95 | `< 120s` |

这些数值是 v1.0 的首批建议线，后续按真实样本表现再做收紧。

---

## 13. 首批实施顺序

### Phase 1

- 补 `tests/eval/retrieval` deterministic dataset
- 跑 summary / fact / key memory recall 的 Recall@K
- 补 wrong-user leakage / stale fact hard assertions

### Phase 2

- 补 extraction eval
- 补 ranking ablation
- 把 score breakdown 导出为 JSON artifact

### Phase 3

- 补 inject vs transparent answer eval
- 接入 nightly real-model run
- 把结果回写到 CI artifact 和 release checklist

### Phase 4

- 用 manager playground 的实际 trace 做抽样 spot check
- 把人工 spot check 模板固化为发版 SOP

---

## 14. 当前结论

在 v1.0 阶段，MemStation 不应只满足“功能可用”，还必须具备：

- 可重复的 recall accuracy 评测
- 可追踪的排序分解
- 可验证的 stale / superseded 防护
- 可量化的 inject 增益
- 可在 manager 中人工复核的 debug 视图

下一步实现时，以 `tests/eval` + `scripts/` 为主落位；manager 只负责提供观测与人工验证入口。
