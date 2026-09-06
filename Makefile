# meept-bench — outcome-level benchmark harness for meept.
#
# Suite targets run the named suite against the live daemon (MEEPT_BENCH_SOCKET
# or default socket) and write results under results/<out>. All targets build
# the runner first, so a stale binary is never used.
#
# Common variables (override on the command line):
#   BENCH_BIN   runner binary path      (default /tmp/meept-bench-bin)
#   REPO        repo under test        (default ~/git/meept)
#   MODEL       force a model for all tasks, e.g. local/lfm-8b-q4
#               (default: unset → daemon default chain; the DEV gate sets
#               this to the local 8B so pushes never burn cloud quota)
#   ATTEMPTS    attempts per task      (default 2)
#   JUDGE_CMD   llm_judge command      (default scripts/judge-local.py via
#               JUDGE_URL/JUDGE_MODEL env — deterministic local judge)

BENCH_BIN ?= /tmp/meept-bench-bin
REPO      ?= $(HOME)/git/meept
SCRATCH   ?= $(HOME)/.meept-bench
ATTEMPTS  ?= 2
JUDGE_CMD ?= python3 scripts/judge-local.py

# Model override: DEV_GATE=1 forces the local 8B (quota-free, deterministic);
# passing MODEL explicitly wins over DEV_GATE.
DEV_GATE ?= 0
ifeq ($(DEV_GATE),1)
MODEL ?= local/lfm-8b-q4
endif
MODEL_ARG := $(if $(MODEL),--model $(MODEL),)

# Flags shared by every suite run.
FLAGS = --repo $(REPO) --scratch $(SCRATCH) --attempts $(ATTEMPTS) \
        --auto-approved $(MODEL_ARG) --judge-cmd "$(JUDGE_CMD)"

.PHONY: all build smoke regression memory-fragments routing-fragment \
        harness-fragment steering gate lmeval diff clean

build:
	go build -o $(BENCH_BIN) ./cmd/meept-bench

# Every committed suite, sequentially. LongMemEval (generated, 500 tasks)
# is NOT in `all` — run it explicitly via `make lmeval` (multi-hour).
all: smoke regression routing-fragment memory-fragments harness-fragment

## Individual suites
smoke: build
	$(BENCH_BIN) run --suite suites/smoke.json --out results/smoke $(FLAGS)

regression: build
	$(BENCH_BIN) run --suite suites/regression.json --out results/regression $(FLAGS)

routing-fragment: build
	$(BENCH_BIN) run --suite suites/fragments/routing-file-write.json --out results/routing-fragment $(FLAGS)

memory-fragments: build
	$(BENCH_BIN) run --suite suites/fragments/memory-recall.json --out results/memory-fragments $(FLAGS)

harness-fragment: build
	$(BENCH_BIN) run --suite suites/fragments/harness-integrity.json --out results/harness-fragment $(FLAGS)

steering: build
	$(BENCH_BIN) run --suite suites/steering.json --out results/steering $(FLAGS)
	@echo "note: steering tasks are environment-gated (agnes quota parks agents)"

## LongMemEval full run — multi-hour; requires the generated suite
## (make lmeval-emit first if suites/longmemeval-s.generated.json is absent).
lmeval: build
	$(BENCH_BIN) run --suite suites/longmemeval-s.generated.json \
		--out results/lmeval-full --auto-approved \
		--judge-cmd "$(JUDGE_CMD)"
	@echo "note: judge is local (quota-free); agent work uses the daemon default chain"

## Regenerate the LongMemEval manifest from the pinned HF revision.
lmeval-emit: build
	$(BENCH_BIN) lmeval -config /tmp/lmeval-full-config.json

## Dev push gate: regression suite on the LOCAL 8B (no cloud quota).
## Used by meept's pre-push hook; clouds are benchmark-only.
gate: build
	$(BENCH_BIN) run --suite suites/regression.json \
		--out results/gate-pre-push $(FLAGS)
	$(BENCH_BIN) diff --baseline results/baseline/regression.jsonl \
		--current results/gate-pre-push/results.jsonl

## Diff any two result sets: make diff BASELINE=... CURRENT=...
diff: build
	$(BENCH_BIN) diff --baseline $(BASELINE) --current $(CURRENT)

clean:
	rm -rf results/smoke results/regression results/gate-pre-push
