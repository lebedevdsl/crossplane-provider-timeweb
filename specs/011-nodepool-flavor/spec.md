# Feature Specification: Nodepool Worker Configurator Flavor

**Feature Branch**: `011-nodepool-flavor`

**Created**: 2026-06-23

**Status**: Draft

**Input**: User description: "Add a `flavor` selector to KubernetesClusterNodepool worker sizing (standard/dedicated-cpu) so operators pick the worker configurator family explicitly, instead of the resolver's tightest-fit silently landing on dedicated-cpu."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Small worker pool provisions without a hidden ratio rejection (Priority: P1)

An operator declares a custom-sized worker pool with a modest CPU/RAM ratio (e.g. 2 CPU / 2 GB) and expects it to provision, the same way the cloud panel's default ("Premium NVMe") accepts it.

**Why this priority**: This is the regression that motivated the feature. Today the provider auto-selects the dedicated-CPU configurator family (which enforces a hidden ~4 GB-per-CPU floor) over the general family, so small/cheap pools are rejected upstream with `invalid_configuration_ram` even though the same sizing is valid on the general family. Without this, common small staging/dev pools cannot be expressed.

**Independent Test**: Create a nodepool with `resources: {cpu: 2, ramGB: 2, diskGB: 40}` and no flavor set; confirm it resolves to the general family and reaches Ready, with no upstream ratio rejection.

**Acceptance Scenarios**:

1. **Given** a nodepool with a custom `resources` block and no `flavor` field, **When** it is created, **Then** the provider resolves the worker configurator from the general family and the upstream create is accepted.
2. **Given** a 2 CPU / 2 GB nodepool with the default flavor, **When** it provisions, **Then** the pool reaches Ready without an `invalid_configuration_ram`-class rejection.

---

### User Story 2 - Operator opts into a dedicated-CPU worker pool (Priority: P2)

An operator who needs dedicated (non-shared) CPU workers explicitly requests that family.

**Why this priority**: Restores access to the dedicated family that the auto-pick previously surfaced only by accident; makes the choice deliberate and declarative.

**Independent Test**: Create a nodepool with `flavor: dedicated-cpu` and a ratio valid for that family (e.g. 2 CPU / 8 GB); confirm it resolves to the dedicated-CPU family and reaches Ready.

**Acceptance Scenarios**:

1. **Given** a nodepool with `flavor: dedicated-cpu` and a flavor-valid sizing, **When** it is created, **Then** the provider resolves a dedicated-CPU configurator and the create is accepted.
2. **Given** `flavor: dedicated-cpu`, **When** the configurator is selected, **Then** a general-family configurator is never chosen, regardless of which family would be a "tighter" numeric fit.

---

### User Story 3 - Clear error when sizing is invalid for the chosen flavor (Priority: P3)

When the requested CPU/RAM/disk cannot be satisfied by any configurator in the chosen flavor, the operator gets an actionable error that names the flavor and the unmet constraint — instead of a silent fall-through to a different family that then fails upstream with a confusing message.

**Why this priority**: Turns a misleading upstream rejection into a diagnosable provider-side condition; lower priority because it is an error-path refinement, not the happy path.

**Independent Test**: Set `flavor: dedicated-cpu` with a sizing below that family's floor and confirm the resource surfaces a clear condition naming the flavor and the constraint, and that no other family is substituted.

**Acceptance Scenarios**:

1. **Given** `flavor: dedicated-cpu` with a sizing below the family floor, **When** the provider resolves the configurator, **Then** it fails with a message naming the flavor and the unmet sizing constraint.
2. **Given** any flavor selection, **When** resolution fails, **Then** the provider does NOT substitute a configurator from a different flavor.

---

### Edge Cases

- **Omitted flavor**: treated as `standard` (general family) — the documented default.
- **Existing pools created before this field**: already provisioned pools keep their locked configurator and are not re-resolved or disrupted by the new default.
- **Sizing valid in more than one family**: the chosen flavor decides; the selection never crosses to another family to get a "tighter" fit.
- **Flavor set on a preset-sized pool** (no custom `resources`): flavor only governs custom-configurator selection; with a preset it has no effect (preset already fixes the family) — see Assumptions.
- **Unknown flavor value**: rejected at admission (enum-constrained), not at reconcile time.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The nodepool worker sizing MUST accept an optional `flavor` field constrained to the values `standard` and `dedicated-cpu`.
- **FR-002**: When `flavor` is omitted, the system MUST behave as if `standard` was selected.
- **FR-003**: `standard` MUST select from the general worker configurator family; `dedicated-cpu` MUST select from the dedicated-CPU worker configurator family.
- **FR-004**: The worker configurator selection MUST be constrained to the chosen flavor's family BEFORE any best-fit/auto-selection step, so the resolved configurator always belongs to the requested family.
- **FR-005**: If no configurator in the chosen flavor can satisfy the requested CPU/RAM/disk, the system MUST fail with a clear error that names the flavor and the unmet constraint, and MUST NOT substitute a configurator from a different flavor.
- **FR-006**: The `flavor` field MUST exist only on the worker (nodepool) sizing; the cluster master sizing MUST NOT gain a flavor field (the master catalog has a single family).
- **FR-007**: The field MUST be backward compatible: existing nodepool definitions without `flavor` remain valid, and already-provisioned pools are not forced to re-resolve or change their locked configurator.
- **FR-008**: An unrecognized `flavor` value MUST be rejected at admission time, before reconciliation.

### Key Entities

- **Worker flavor**: an operator-facing selector on a nodepool's custom sizing with two values — `standard` and `dedicated-cpu` — each corresponding to one upstream worker configurator family.
- **Configurator family**: the upstream catalog partition of worker configurators (general vs dedicated-CPU). Each family has its own per-CPU RAM ratio and bounds; `standard` ≈ shared-CPU/general (lower RAM-per-CPU floor), `dedicated-cpu` ≈ dedicated-CPU (higher RAM-per-CPU floor).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A nodepool sized 2 CPU / 2 GB with the default flavor provisions to Ready with zero upstream `invalid_configuration_ram` rejections.
- **SC-002**: An operator can provision a dedicated-CPU worker pool solely by setting `flavor: dedicated-cpu` (no other field changes).
- **SC-003**: 100% of flavor selections resolve to a configurator in the matching family — never a cross-family configurator.
- **SC-004**: When a sizing is invalid for the chosen flavor, the resulting error message identifies both the flavor and the unmet constraint, with no silent cross-family substitution.
- **SC-005**: Existing nodepool manifests authored before this feature continue to apply and reconcile without modification.

## Assumptions

- `standard` (general family) is the correct default — it matches the cloud panel's default tab and the broadest, lowest-ratio sizing envelope, so the common small-pool case "just works".
- Exactly two worker families are exposed as flavors: general (`standard`) and dedicated-CPU (`dedicated-cpu`). GPU configurators are out of scope (GPU is a separate count axis, not a flavor).
- The master sizing has a single configurator family upstream, so no flavor selection applies there.
- `flavor` governs only custom-configurator (`resources`) selection. Preset-sized pools already fix their family via the preset and are unaffected.
- The `flavor` field follows the same create-time/change semantics as the existing `resources` CPU/RAM/disk fields (changing it is governed by the existing nodepool resize behavior, not introduced here).
- The prior round's fixes (master custom-configurator `gpu: null`, worker `gpu` omit-unless-positive, and the autoscaling `min_size`/`max_size` field-name correction) are already shipped (v0.3.1) and present in the running provider; they are background context, not part of this feature.
