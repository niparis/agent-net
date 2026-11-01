# 0045: Worker maximum level

The owner requires: "no model should be used for more than 1 level less than its max".

A profile's max is the highest difficulty whose minimum its score meets,
including owner overrides and the resolved effort. Automatic choice permits
only that level and exactly one below. This replaces the unlimited adequate
range in decision 0026. The exponential score penalty still applies among
eligible profiles, after this exclusion.

No eligible profile means an actionable error with the level, roster and
max levels. There is no over-qualified or empty-roster default fallback.
An explicit model overrides eligibility and the start note explains when
it does. Automatic notes identify exclusions. The Quotas forecast uses the
same eligibility rule and marks excluded cells with x.
