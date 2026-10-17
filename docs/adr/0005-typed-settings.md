# Persist typed settings separately from remote content

The daemon owns typed settings and saved subscription snapshots in private root-owned state. It builds the running configuration from those settings and an allowlisted subscription, so downloads cannot change listeners, filesystem paths or host privileges.

User rules run before subscription rules and survive refreshes. Arbitrary YAML overrides are unsupported: preserving that escape hatch would let the owner configure a privileged proxy outside the settings boundary. Cutover translates supported override fields and typed rules once; the old files remain recovery data, never a runtime input.
