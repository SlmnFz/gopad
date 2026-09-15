# CRDT benchmark baseline

Baseline captured on 2026-09-15 with:

```text
go test ./internal/crdt -bench=. -benchmem -run=^$ -benchtime=1x
```

Host: Windows amd64, 11th Gen Intel(R) Core(TM) i7-1165G7 @ 2.80GHz.
Each row is one workload containing the named number of total operations.

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Apply 1k / 0% deletes | 197,100 | 324,408 | 20 |
| Apply 1k / 30% deletes | 125,500 | 160,488 | 15 |
| Apply 1k / 70% deletes | 167,000 | 160,504 | 16 |
| Apply 10k / 0% deletes | 1,474,500 | 2,619,288 | 79 |
| Apply 10k / 30% deletes | 952,000 | 1,635,864 | 55 |
| Apply 10k / 70% deletes | 774,800 | 1,307,928 | 46 |
| Apply 100k / 0% deletes | 17,645,100 | 20,978,456 | 530 |
| Apply 100k / 30% deletes | 16,169,400 | 20,978,456 | 530 |
| Apply 100k / 70% deletes | 9,407,500 | 10,487,448 | 273 |
| Apply 1M / 0% deletes | 386,189,000 | 334,561,880 | 8,186 |
| Apply 1M / 30% deletes | 237,942,000 | 167,853,096 | 4,118 |
| Apply 1M / 70% deletes | 162,634,200 | 167,361,480 | 4,107 |
| Text 1k / 0% deletes | 458,400 | 185,352 | 24 |
| Text 1k / 30% deletes | 338,500 | 167,720 | 22 |
| Text 1k / 70% deletes | 211,500 | 89,560 | 15 |
| Text 10k / 0% deletes | 6,259,500 | 2,960,648 | 38 |
| Text 10k / 30% deletes | 3,898,500 | 1,699,848 | 33 |
| Text 10k / 70% deletes | 2,957,200 | 1,195,480 | 21 |
| Text 100k / 0% deletes | 78,866,700 | 34,694,424 | 58 |
| Text 100k / 30% deletes | 57,417,600 | 21,518,536 | 56 |
| Text 100k / 70% deletes | 36,494,800 | 16,154,072 | 31 |
| Text 1M / 0% deletes | 944,308,000 | 339,600,648 | 77 |
| Text 1M / 30% deletes | 733,412,800 | 214,304,200 | 73 |
| Text 1M / 70% deletes | 450,279,900 | 161,242,584 | 41 |

These are directional baselines, not performance guarantees. Compare future
changes using the same host, Go version, and benchmark command.

## Task 13 tombstone-compaction comparison

The original Task 11 table above was one run. A fresh pre-Task 13 baseline
rerun was captured immediately before implementing compaction, followed by a
post-change run of the compacted variant. Both use the same host and
`-benchtime=1x`; each reported value is one benchmark iteration. The compacted
variant simulates four snapshot cycles and only compacts tombstones from the
previous cycle, matching the production one-snapshot grace window.

Command used for the fresh baseline:

```text
go test ./internal/crdt -run '^$' -bench 'BenchmarkApply|BenchmarkText' -benchmem -benchtime 1x -count 1 -v
```

Command used after Task 13:

```text
go test ./internal/crdt -run '^$' -bench 'BenchmarkApplyCompacted|BenchmarkTextCompacted' -benchmem -benchtime 1x -count 1 -v
```

Host: Windows amd64, 11th Gen Intel(R) Core(TM) i7-1165G7 @ 2.80GHz.

The values below are `ns/op`, `B/op`, and `allocs/op`. Lower is better. The
compacted Apply benchmark includes the cost of compaction; the compacted Text
benchmark measures reading the resulting compacted document.

| Workload | Baseline Apply | Compacted Apply | Baseline Text | Compacted Text |
| --- | ---: | ---: | ---: | ---: |
| 1k / 30% deletes | 124,100 / 181,608 / 15 | 211,700 / 211,888 / 29 | 967,700 / 441,704 / 1,438 | 566,800 / 343,560 / 1,017 |
| 1k / 70% deletes | 104,800 / 181,608 / 15 | 159,400 / 145,808 / 28 | 654,600 / 339,608 / 1,031 | 206,700 / 84,408 / 289 |
| 10k / 30% deletes | 968,600 / 1,804,440 / 55 | 1,421,500 / 1,882,720 / 66 | 14,667,600 / 5,151,848 / 14,149 | 9,083,300 / 3,453,448 / 9,604 |
| 10k / 70% deletes | 832,700 / 1,443,736 / 46 | 2,267,800 / 1,601,824 / 51 | 9,395,900 / 3,452,600 / 10,093 | 1,574,200 / 814,040 / 2,541 |
| 100k / 30% deletes | 18,218,100 / 23,080,344 / 530 | 23,877,100 / 18,939,504 / 345 | 253,006,500 / 56,893,032 / 141,083 | 135,185,300 / 33,520,040 / 95,569 |
| 100k / 70% deletes | 11,383,700 / 11,540,776 / 274 | 19,425,400 / 17,735,520 / 232 | 150,592,800 / 36,669,240 / 100,551 | 27,831,900 / 8,696,328 / 25,162 |
| 1M / 30% deletes | 280,221,800 / 184,635,224 / 4,119 | 382,400,600 / 246,348,128 / 4,156 | 3,495,676,700 / 522,095,976 / 1,408,271 | 2,106,017,300 / 440,377,832 / 958,270 |
| 1M / 70% deletes | 190,282,300 / 184,274,456 / 4,109 | 470,077,900 / 191,329,424 / 2,112 | 2,315,623,600 / 436,177,464 / 1,008,241 | 468,486,600 / 109,859,256 / 252,091 |

At the 1M workloads, compaction makes the read path substantially cheaper:
Text at 30% deletes is about 40% faster with 16% fewer bytes allocated, while
70% deletes is about 80% faster with 75% fewer bytes allocated. Apply includes
the intentional compaction work, so it is slower in this synthetic workload;
that is the tradeoff this comparison is meant to make visible.

The compaction safety window is deliberately conservative: only tombstones
from the previous successful snapshot are requested for removal. This is a
latency heuristic, not a causal-stability proof. The durable operation log is
not compacted and remains available for history/replay features.
