# LongMemEval License & Dataset Verification

Task-1 record for leaf 08 (docs/plans/phase-2/08-longmemeval-adapter.md).
Verified 2026-09-01 against the HuggingFace Hub API. Per
docs/BENCHMARKS.md rule 1, re-verify at every future integration:

```
curl -s https://huggingface.co/api/datasets/xiaowu0162/longmemeval | jq '.gated, .cardData.license, .tags, .sha'
```

## Verdict: CLEARED (MIT)

The license permits use, redistribution, and derivative works, subject to
the MIT notice preservation condition. The adapter and its generated
manifests are therefore allowed — but we keep the stricter BENCHMARKS.md
hygiene rules (no dataset content committed; manifests generated at run
time and gitignored).

## Evidence

| Fact | Value | Source |
|------|-------|--------|
| Dataset ID | `xiaowu0162/longmemeval` (lowercase; the old `LongMemEval` path redirects here) | Hub API `id` field |
| License | **MIT** | Hub API `cardData.license: "mit"` and dataset tag `license:mit` (both stated in the README.md card front-matter on the Hub) |
| Gated | `false` (no access request, no token required) | Hub API `gated` field |
| Revision verified | `2ec2a557f339b6c0369619b1ed5793734cc87533` (`lastModified` 2025-09-19) | Hub API `sha` field |
| Deprecation notice | The card description says the dataset is **deprecated in favor of `xiaowu0162/longmemeval-cleaned`** (noisy history sessions interfere with answer correctness). Not a licensing problem; a quality note. | Hub API `description` |
| Configs/splits | Single config `default`; split files `longmemeval_oracle`, `longmemeval_s`, `longmemeval_m` | card `configs[].data_files` |

## Split S shape

- File: `longmemeval_s` (extensionless; a single top-level JSON **array**,
  ~278 MB at the verified revision). Sibling splits: `longmemeval_oracle`
  (~15 MB), `longmemeval_m` (~2.7 GB).
- Item count: 500 items (per the LongMemEval paper / repo; not re-counted
  this session — fetching the full file was out of budget).
- Item fields (documented schema, consistent with the
  [IBM/LongMemEval](https://github.com/IBM/LongMemEval) repo):
  `question_id`, `question_type`, `question`, `answer`, `question_date`,
  `haystack_date`, `haystack_sessions` (array of sessions, each an array of
  `{role, content}` turns), `haystack_session_ids`,
  `answer_session_ids`, plus `quiz` in some revisions. The adapter decodes
  defensively (unknown fields ignored, session turns rendered from
  recognized keys with JSON fallback).

## Access path (what the adapter actually does)

The datasets-server **/rows API does NOT work** for this dataset: its data
files are extensionless JSON, so the parquet-on-the-fly worker fails to
discover them (`/splits` returns `FileNotFoundError ... 'longmemeval_oracle.json'`;
`/rows` returns `{"error":"Not found."}` — probed unauthenticated,
2026-09-01). The adapter therefore offers two fetch methods:

1. `method: "download"` (default): `GET
   https://huggingface.co/datasets/<id>/resolve/<revision>/longmemeval_s`
   and streams the JSON array with a bounded decoder, stopping early once
   `limit` items are read (a 5-item fetch does not download 278 MB).
   Works unauthenticated for this public dataset.
2. `method: "rows"`: datasets-server paging — only usable if HF later
   converts the dataset / for converted mirrors. Honored per the leaf
   spec; not expected to work today.

Auth: the adapter attaches `Authorization: Bearer $HF_TOKEN` when the
environment variable is set; never required for this dataset (not gated).
Config lives at run time (`suites/longmemeval.local.json` is already
covered by the repo-wide `*.local.json` gitignore rule).

## Config file schema (CLI `meept-bench lmeval --config ...`)

| Key | Type | Default | Meaning |
|-----|------|---------|---------|
| `dataset_id` | string | (required) | HF dataset id, e.g. `xiaowu0162/longmemeval` |
| `revision` | string | resolve latest | Pin the commit sha for reproducibility |
| `split` | string | `S` | `S`, `M`, or `oracle` |
| `limit` | int | 0 (all) | Number of items to emit; template validation uses 5 |
| `mode` | string | `context` | `context` (haystack → data files) or `memory` (haystack inline in prompt) |
| `seed` | int | 0 | Nonzero → deterministic reservoir sample over the whole split |
| `method` | string | `download` | `download` or `rows` |
| `config_name` | string | `default` | datasets-server config (method `rows` only) |

## Licensing notes for this repo

- The MIT notice must accompany substantial copies of the data. We do not
  redistribute the data; generated manifests reference the upstream
  source and record the revision hash (`hf_revision` on the manifest,
  `lmeval-rev:<sha>` tag on every task → every result row).
- Generated artifacts (`suites/longmemeval-s.generated.json`,
  `suites/lmeval-data/`) are gitignored; only the **template** suite
  (`suites/longmemeval-s.template.json`) and **synthetic** fixture data
  (`suites/lmeval-data-template/`) are committed.
- The manifest is emitted with `internal: true` (BENCHMARKS.md rule 4:
  research benchmarks tracked internally; published scorecards must
  follow the paper's licensing/leaderboard rules).
- Upstream notice: the paper ("LongMemEval: Benchmarking Chat Assistants
  on Long-Term Interactive Memory", Wu et al., 2024) is MIT-licensed as a
  codebase (IBM/LongMemEval repo); cite the paper when publishing scores.
