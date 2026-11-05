# Memory quality research

MemStation targets near-state-of-the-art LOCOMO answer quality with bounded,
explicitly measured runtime model cost. This is a research and
acceptance method, not a claim that the target has been reached. Per-run results,
source snapshots, candidate patches and rejected experiments belong in ignored
local archives; raw datasets, model responses and credentials stay private.

## Define the comparison before optimizing

Keep two records: an external research reference and our controlled measurement.
For each external result, record the primary source and revision, dataset split,
question count, modality, ingestion inputs, memory implementation, reader/judge,
prompts, retrieval depth, context size, extra model calls and available artifacts.
Mark hosted-only results, unreported settings and internal inconsistencies.
Recall@k, answer accuracy, F1 and judge agreement are different measurements.

For this project, the proposed operational meaning of **near SOTA** is an answer
accuracy gap of at most two percentage points from the strongest reproducible
reference under a matched protocol, accompanied by uncertainty and the complete
cost comparison. It is a project target, not an industry-standard threshold.
If the model, judge, modality or question scope cannot be matched, report the
external number as context and leave the parity claim unverified. A small
failure-selected gain never establishes this target.

Runtime costs include retrieval models, reranking, planning, refinement and
reader input/output. Report ingestion amortization and offline judging separately.
A larger model, repeated reasoning or more context must not silently purchase
the improvement. Candidate count and byte caps are safeguards; actual tokens,
calls, latency and, when available, monetary cost determine the cost outcome.
Quality has priority within the agreed experiment budget. When a modest token
increase is explicitly accepted, do not keep rejecting it under an obsolete
no-increase condition. Record the accepted tradeoff before the next experiment;
it does not waive control regressions or authorize unbounded model spending.

## Turn framework research into testable changes

Refresh primary sources at the start of each substantive quality iteration.
Record what changed since the previous audit. A paper, current source code and
a hosted service can describe different implementations.

| Reference | Reusable idea | MemStation experiment boundary |
| --- | --- | --- |
| [Hindsight retrieval](https://hindsight.vectorize.io/developer/retrieval) | Fuse independent retrieval ranks before cross-encoder scoring; budget returned facts and optional original chunks separately | Use existing authorized channel results; keep final relevance, importance and time semantics separate from admission and source expansion |
| [Graphiti search recipes](https://github.com/getzep/graphiti/blob/main/graphiti_core/search/search_config_recipes.py) | Explicit hybrid, RRF, MMR and graph traversal recipes | Test admission diversity without claiming a deterministic heuristic is a model reranker |
| [EverMemOS paper](https://arxiv.org/html/2601.02163v2) | Organize complete episodes and recover original context through scene-oriented retrieval | Reuse indexed sources and provenance first; additional LLM segmentation or sufficiency loops need their own cost justification |
| [Mem0 algorithm](https://mem0.ai/blog/mem0-the-token-efficient-memory-algorithm) | Combine sentence/entity retrieval and preserve historical observations | Entity mentions remain routing signals, not attributed personal facts; retain explicit validity and supersession semantics |
| [Zep evaluation methodology](https://www.getzep.com/research/) | Retrieve facts, entities, episodes, observations and summaries as distinct scopes | Measure the useful evidence contributed by each scope; do not assume identical record counts imply identical information or cost |
| [EverOS multi-round retrieval](https://github.com/EverMind-AI/EverOS/blob/962c63eeaae5093b495a4763c161f8b046d41bc2/src/everos/memory/search/llm_multiround.py) | Preserve complementary evidence across query facets and retain selected core evidence | This version differs from the EverMemOS paper and omits cross-encoder reranking; its LLM decider is an additional cost, not a free replacement for our current model |

Each experiment records one hypothesis, the implementation delta, its expected
failure mechanism, fixed sample and budget, promotion criteria, outcome and next
decision. Preserve negative results. A follow-up hypothesis gets a new freeze;
do not overwrite the failed attempt or relabel a combined policy change as a
single-variable ablation. Borrow the mechanism, not dataset/person rules.

### Candidate admission is not relevance

Keep retrieval ranks, inference admission, neural relevance and evidence packing
separate. RRF can combine independent ranked channels without comparing their
raw score scales, but it cannot decide whether an unscored conversation companion
answers the question. A conversation quota supplies coverage, not relevance.
If a hydrated candidate has no original query score, evaluate a query-dependent
signal before granting it a scarce model slot; do not assign a fixed semantic
bonus because it shares a conversation with a strong hit.

For a bounded lexical admission pass, use only the current question and the
authorized candidate body texts. Record identifiers, annotated source IDs,
historical grades and expected answers must remain outside that pass. Preserve
distinct source identities even when their texts are equal. Record lexical
matching limits explicitly: exact tokens do not establish paraphrase, entity
attribution, morphological equivalence or event-time reasoning.

A question-scoped conversation lookup should apply authorization, generation,
conversation and type constraints before scoring and limiting, then hydrate only
the selected records. Morphological normalization can connect inflected words,
but it does not resolve which speaker owns an aspiration or whether an event
happened. Keep the direct retrieval channels intact and measure any displacement.
Native [PostgreSQL full-text search](https://www.postgresql.org/docs/16/textsearch-controls.html)
provides dictionaries and lexemes; a weighted term-coverage implementation must
not be described as BM25. In-memory and SQL backends need shared scope and
normalization fixtures, with tokenization differences stated explicitly.

An admission-only policy must leave canonical content and final scoring factors
unchanged. Model failures must retain the existing failure contract, and a model
score of zero must remain zero regardless of any admission reservation. Compare
newly admitted evidence with displaced records, then verify that useful evidence
survives packing. Success at either boundary is insufficient for replacement
without the protected answer-quality comparison.

## Cheap evidence before inference

1. Inspect the existing failures and a small fixed set of previous successes.
   Record which conversations have already been used for development.
2. Reuse exact query-provider outputs against a read-only corpus. Capture
   collection, admission, rerank eligibility, packed evidence and final answers
   as separate stages. A zero-model capture is not a reranker failure benchmark.
3. Check previously supported information, new failure evidence, source scope,
   candidate count and total bytes. Source-reference coverage is a diagnostic;
   it neither proves entailment nor detects every fact preserved in a summary.
   Compare record identities, types and content hashes across the baseline and
   candidate as well: unchanged label coverage can hide the loss of a useful
   summary. Diagnose missing records offline; never feed historical grades,
   previous model scores or case-specific retained IDs into product admission.
4. Only after evidence and cost gates pass, spend the frozen reranker budget.
   Recheck the packed context before running all cases in the paired QA cohort.
   Keep prompts, models, grading and arm order policy fixed, with no selective
   retries or rejudging. Retain errors and unexecuted rows in the denominator.
5. Compare reader costs separately from judge costs. Reject candidates that
   violate the predefined quality, successful-control or runtime cost conditions.

Track evidence at collection, model admission, rendered reranker input and final
packet separately. A highly scored original record can still be displaced during
packing. Recovering that record is a coverage improvement, not automatically a
correct-answer improvement: a multi-part answer may still lack another event,
relationship or date. Preserve both measurements and the original grades.
When a reference conflicts with the declared source, keep the failing row and
document the conflict separately; do not teach the reader to repeat the conflict.

For temporal retrieval, distinguish a date mentioned in source text, a source
timestamp, an event date and the time a record was ingested. Calendar arithmetic
may locate an original passage, but it must not manufacture a stored event fact
or an answer hint. Test invalid dates, ambiguous anchors, negation, overlapping
source windows and queries without dates. Verify that the relevant passage
survives final packing as well as candidate admission.

Include the largest next-call reservation when budgeting a complete paired run.
If a cost guard stops a run, preserve every unexecuted row and keep its result
separate. A later confirmation repeats the complete frozen cohort under a new
declared budget; it does not selectively fill or replace the failed attempt.

The executable helpers and their exact inputs are described in the
[evaluation guide](../apps/server/tests/eval/README.md). Frozen per-document
rerank scores support only exact matching inputs/subsets; missing scores cannot
be fabricated or filled by an unbudgeted provider call.
For an excerpt-based reranker, reconstruct the verified request-local view before
checking cached score identity. Compare the actual rendered bytes, not only the
canonical parent text or node ID, and reject unverifiable source revisions/ranges.

## Isolate reader completeness

A source may be retrieved and packed intact while its purpose, outcome or
condition disappears during answer synthesis. Treat distinct relevant
propositions as separate information; an activity does not automatically express
its purpose, and one benefit does not cover another. Preserve modality and
attribution rather than filling gaps from background knowledge.

Test this as a reader-only comparison: freeze identical context bytes, canonical
records and source mappings, compare the prior and candidate code instructions,
and keep the judge unchanged. Run a separately budgeted empty-memory diagnostic
for both instructions. Calibration on independent synthetic examples tests the
judge against those examples only; it cannot establish benchmark-wide stability
or justify replacing an earlier grade. A source conflict, a reader omission and
a judge discrepancy need separate records.

[Hindsight Reflect](https://hindsight.vectorize.io/developer/reflect) separates
reasoned response generation from recall and uses evidence gathering and citation
validation. Its agentic loop carries additional model/tool cost; a single-call
frozen-evidence reader experiment does not reproduce that architecture or its
results. Likewise, LOCOMO's upstream
[evaluation implementation](https://github.com/snap-research/locomo/blob/main/task_eval/evaluation.py)
includes token-overlap F1; that metric and our frozen reference-agreement judge
are different protocols. Report the chosen protocol, not an interchangeable
"LOCOMO score."

## Validate evidence organization

Record selection order and rendered order can differ. A formatter that groups
tasks, facts and original turns into separate sections can undo source adjacency.
Measure positions in the actual reader input. If an experiment removes those
sections as well as moving records, declare both changes in the frozen hypothesis.
Keep per-record type and labels, canonical content, provenance and opaque source
bindings intact. Compare full records at preparation and before inference;
matching only IDs and body text cannot detect changed attribution or revisions.

Source association requires explicit provenance within the same authorized
scope. Revisioned source coordinates must match the message, revision and part,
with the supplied span containing the cited span; they must not fall back to
sequence metadata on a mismatch. Sequence-based quotes require an explicit
coordinate base and an exact quote in the original turn. Shared conversation,
speaker or topic alone is not evidence of support. A source can be shared by
multiple memories without being emitted repeatedly.

An intact and adjacent source can still lose a purpose, ingredient, condition or
other concrete detail during synthesis. Source proximity and record preservation
are structural checks, not proof of answer completeness. Inspect the original
answers when grades differ: if both omit the same proposition but only one fails,
retain both frozen grades and record the inconsistency separately. Do not retry
or change the judge to turn that disagreement into a candidate gain. Passing a
small synthetic calibration does not resolve this benchmark-level uncertainty.

## Validate source views independently

A shorter source representation needs both provenance and coverage checks.
For a lossless representation, reconstruct every original turn byte for byte,
including its speaker, date, order, negation and provisional language. Verify
source revision, field, byte ranges, UTF-8 boundaries and full-field hashes.
Partial fields or unverifiable metadata cannot support a whole-turn proof.

Lossless rendering of each admitted record does not prove that admission still
retains the useful records. Check the complete fixed cohort after applying the
input cap; a shorter representation can still lose a successful control's
source. Do not trade this failure away by reporting only aggregate byte savings.
Any changed reranker text requires fresh scores if the candidate passes the
offline gates. Reconstruction alone does not establish that a model interprets
the representation correctly.

For an excerpt-based view, report the omitted source ranges explicitly and
audit the actual rendered evidence rather than the full parent record's
provenance. Keep necessary attribution and adjacent context within the frozen
budget. Never call selective extraction lossless compression.

## Expand only after a candidate earns it

Successful development replays justify a bounded validation on conversations
that have not been used for tuning. Freeze those IDs, source processing, modality,
models, prompts, budgets and scoring before examining outcomes. Keep their scores
separate from the repeatedly inspected development cohort.

After that validation succeeds, budget one complete protocol-aligned LOCOMO
run. For the common non-adversarial comparison this means all 1,540 questions,
including questions with missing evidence labels and execution failures. Retain
all raw results and category/conversation breakdowns. Report uncertainty with
the conversation-level dependence acknowledged, and obtain independent review
of disputed or decisive judgments without replacing the original scores.

The final report must identify which reference was matched, the accuracy gap,
cost/latency differences, development exposure and remaining protocol differences.
Until these conditions hold, report measured local progress rather than near-SOTA
parity. Quality benchmarks remain manual diagnostics, not default CI gates.
