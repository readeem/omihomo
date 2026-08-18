# User edits live in an override layer, never in the fetched YAML

Rules, TUN settings, and fixed ports all live in the same YAML that a subscription refresh
overwrites. Editing in place works until the first update silently eats your rules.

The fetched YAML is treated as a pure cache that can always be re-downloaded. User edits live
in a separate, global override file and are merged on every activation to produce the YAML
mihomo runs. This is mihoro's `[mihomo_config]` idea and Koala's override model, which handles
`proxies`, `proxy-groups`, and `rules` as mergeable arrays with `prepend`, `append`, and
`filter` (`src/main/utils/template.ts:105`).

## Consequences

"Update the subscription" and "I edited my rules" can both be true. Rule editing becomes editing
one small file, so the GUI can offer a form over it rather than embedding a YAML editor. The
override layer is global rather than per-subscription, which is a simplification worth revisiting
if per-subscription rules are ever wanted.
