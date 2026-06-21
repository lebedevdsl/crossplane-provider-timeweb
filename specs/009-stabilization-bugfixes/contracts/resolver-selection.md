# Contract — Configurator selection & error surfacing

## Selection ordering (FR-010)

Given the configurators that pass the existing capability filter (size fits
`requirements.{min,step,max}`):

1. **Partition** by family tag: **standard** (preferred) vs **promo/legacy**
   (deprioritized). Classification by tag-prefix list, not hardcoded ids:
   - standard ⇒ e.g. `*_nvme`, `*_dedicated_cpu`, `*_high_cpu`, plain `ssd`/`nvme`
   - promo/legacy ⇒ e.g. `discount*`, `ssd_2022`, `*promo*`, `*_gpu` (when not GPU-requested), `spb3_dedicated_cpu`
2. Within the chosen partition, keep the existing tightest-fit-by-max →
   lowest-upstream-id tiebreak.
3. **No price computation.**

Standard family MUST be chosen over promo/legacy when both satisfy the size.

## No-orderable error (FR-009)

When the only configurators satisfying the size are promo/legacy (non-orderable),
the resolver MUST return an error that names the real cause — e.g.
`no orderable configurator for <location>/<size> (only promo/legacy entries: …)`
— NOT a misleading phantom-preset error. Builds on the 008 `Classify` 404-body
surfacing so the upstream `response_id` is still available when it does reach the
API.

## Test contract (Constitution III)

Fake-client unit tests MUST cover:
- standard preferred over promo when both fit;
- only-promo-available ⇒ the clear no-orderable error (terminal);
- existing tightest-fit behavior unchanged within a partition.
