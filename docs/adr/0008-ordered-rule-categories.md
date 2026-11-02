# Categories are ordered routing blocks

The rule editor and routing use the same order. User categories and standalone rules share one
ordered list, with ordered rules inside each category. Categories cannot nest. Category IDs stay
stable across renames. Existing flat rule arrays remain valid and retain their order. New categories
are created with their first rule in one transaction; empty user categories are removed automatically.

Disabling a category excludes its rules without changing their individual enabled states. A
category target change is one bulk edit of its current rules, with no inherited destination.
SUB-RULE targets name rule lists, so bulk destination changes leave those targets alone.

Generated Tailscale rules and the active subscription's rules occupy fixed categories after user
rules, in that order. Only their enabled states are editable; downloaded contents are never copied
into user rules. Disabling the Tailscale category does not disable the host integration. Category
toggles survive subscription refreshes and switches. Unmatched traffic retains the engine's existing
fallback behavior.

The daemon compiles the saved document into effective rules and lists disabled and unavailable
rules separately from routing participation. The widget retains those identities when categories
are collapsed. Rules following an enabled MATCH are marked unreachable, including inside collapsed
categories. Collapse state is presentation state and is never sent to the daemon.

Legacy type-family subscription target overrides conflict with read-only subscription contents.
They are rejected explicitly, including on restoring cached settings, rather than silently ignored;
the owner must remove `category_targets` before applying settings or starting the proxy.
