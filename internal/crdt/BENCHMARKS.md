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
