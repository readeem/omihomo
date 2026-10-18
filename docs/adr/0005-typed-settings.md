# Persist typed settings separately from remote content

The owner keeps typed settings and rules in two JSON files in their config directory so dotfile managers can track them. The unprivileged CLI applies them over the owner-only socket; the widget edits through that CLI. The daemon validates them and caches the last accepted copy beside private subscription snapshots so boot does not depend on the owner logging in. It never reads or writes the owner's home. It builds the running configuration from those settings and an allowlisted subscription, so downloads cannot change listeners, filesystem paths or host privileges.

User rules run before subscription rules and survive refreshes. Arbitrary YAML overrides are unsupported: preserving that escape hatch would let the owner configure a privileged proxy outside the settings boundary. Cutover translates supported override fields and typed rules once; the old files remain recovery data, never a runtime input.
