// EvalPlus V4 Pro integrity audit — 4 independent reviewers + synthesis.
// Run AFTER scoring has produced eval/eval_final_*_v4pro.json and samples_*_v4pro.jsonl.
// Each reviewer is an independent agent that must try to REFUTE the validity of the
// V4 Pro scores. Findings that survive are real integrity concerns.

export const meta = {
  name: 'evalplus-v4pro-integrity-audit',
  description: 'Adversarial integrity audit of DeepSeek V4 Pro EvalPlus scores',
  phases: [
    { title: 'Audit', detail: '4 independent reviewers' },
    { title: 'Synthesize', detail: 'merge surviving concerns' },
  ],
}

const REPO = 'D:/vscode/localcode'
const FINDINGS_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['verdict', 'concerns', 'evidence'],
  properties: {
    verdict: { type: 'string', enum: ['CLEAN', 'CONCERNS'], description: 'CLEAN = no standard-lowering found' },
    concerns: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['severity', 'summary', 'evidence'],
        properties: {
          severity: { type: 'string', enum: ['blocker', 'major', 'minor'] },
          summary: { type: 'string' },
          evidence: { type: 'string', description: 'concrete file/line/value proving the concern' },
        },
      },
    },
    evidence: { type: 'string', description: 'summary of what was inspected' },
  },
}

phase('Audit')

const AUDITS = [
  {
    label: 'pipeline-inflation',
    prompt: `You are auditing an eval pipeline for score INFLATION. Repo: ${REPO}. Try hard to find any way the V4 Pro EvalPlus scores could be inflated or the bar lowered. Inspect and report on EACH:
1. eval/gen_samples.py — does it ever weaken the prompt, set base_only, or feed the answer? Confirm temperature=0.0 (greedy pass@1) and that the prompt is held identical to the V3 run.
2. eval/score_sequential.py — confirm base_only=False, fast_check=False (strict). Confirm the import-prefix fix is applied SYMMETRICALLY (not only to V4 Pro). Confirm the old silent 'plus=base' fallback is gone.
3. Count fallback bodies (def X(*a,**k): pass) in eval/samples_humaneval_v4pro.jsonl and eval/samples_mbpp_v4pro.jsonl — a high count means API failures masquerading as model failures (must be re-run).
4. Sanity: open eval/eval_final_humaneval_v3.json and eval/eval_final_humaneval_v4pro.json; assert plus_pass1 <= base_pass1 in both.
Return verdict CLEAN only if you find NO inflation mechanism. Be concrete with file:line.`,
  },
  {
    label: 'mbpp-manual-reverify',
    prompt: `You are manually re-verifying MBPP+ V4 Pro scoring to rule out test contamination (the V3 MBPP base was 93.1%, flagged as possibly high). Repo: ${REPO}.
1. From eval/samples_mbpp_v4pro.jsonl, pick 10 task_ids at fixed intervals (every ~38th). For EACH, read the problem prompt + the model's 'solution'.
2. INDEPENDENTLY derive the expected output for the prompt's visible test by hand-reasoning (do NOT trust evalplus), then run the model's function in a clean 'python -c' subprocess and compare.
3. Cross-check against eval/eval_final_mbpp_v4pro.json per_task (base/plus).
Red flag = any case where the model output is WRONG but evalplus scored base=pass (= contamination), OR right but scored fail. Report the 10 cases with your derived expectation vs actual vs evalplus verdict. Verdict CLEAN only if 0 contradictions.`,
  },
  {
    label: 'humaneval-failure-rootcause',
    prompt: `You are diagnosing WHY V4 Pro failed specific HumanEval+ problems, to ensure the V3->V4Pro delta isn't contaminated by a shared pipeline artifact. Repo: ${REPO}.
1. From eval/eval_final_humaneval_v4pro.json per_task, collect task_ids where base=False.
2. For each failure, read the problem (host: use python -c "from evalplus.data import get_human_eval_plus; print(get_human_eval_plus()[TID]['prompt'])") and the model's solution from eval/samples_humaneval_v4pro.jsonl.
3. Bucket each failure into: (a) import/NameError artifact, (b) wrong logic, (c) timeout/OOM, (d) fallback body, (e) syntax/format.
4. Compute overlap of the failure set with the 23 import-prefix problems (those whose prompt starts with 'from/import').
Report the bucket counts, the list of import-prefix failures, and whether the delta is contaminated. Verdict CLEAN if import-artifact failures are a small minority.`,
  },
  {
    label: 'cross-model-sanity',
    prompt: `You are sanity-checking V4 Pro EvalPlus scores against the public band for comparable reasoning models. Repo: ${REPO}.
1. Read eval/eval_final_humaneval_v4pro.json and eval/eval_final_mbpp_v4pro.json for the actual V4 Pro base/plus pass@1.
2. WebSearch for DeepSeek V4 (Pro) HumanEval+ / MBPP+ public numbers, and for deepseek-reasoner / o1-class HumanEval+ ranges.
3. Judge: are the V4 Pro scores PLAUSIBLE (within or near the public band)? Wildly above suggests leakage/bug; wildly below suggests the import artifact or truncation is biting.
Report the V4 Pro numbers, the public band you found with URLs, and your plausibility judgment. Verdict CLEAN if plausible. Note: this is a sanity prior only, never a target.`,
  },
]

const reviews = await parallel(
  AUDITS.map((a) => () =>
    agent(a.prompt, { label: `audit:${a.label}`, phase: 'Audit', schema: FINDINGS_SCHEMA })
  )
)

const valid = reviews.filter(Boolean)

phase('Synthesize')

const evidence_brief = valid
  .map((r, i) => `--- ${AUDITS[i].label} ---\nverdict: ${r.verdict}\n${r.evidence}\nconcerns: ${JSON.stringify(r.concerns)}`)
  .join('\n\n')

const synthesis = await agent(
  `You are the synthesis lead for an eval integrity audit. Four independent reviewers audited DeepSeek V4 Pro EvalPlus scores. Their structured findings:

${evidence_brief}

Produce a final integrity verdict: is there ANY standard-lowering, inflation, or contamination? List surviving concerns (drop anything unverified or speculative) ranked by severity. If everything is clean, say so explicitly. Be concise.`,
  { label: 'synthesize', phase: 'Synthesize' }
)

return { reviews: valid.map((r, i) => ({ audit: AUDITS[i].label, ...r })), synthesis }
