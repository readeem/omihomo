# Clients share one daemon socket

The widget and Go CLI talk to the daemon over an owner-only Unix socket with JSON lines and push streams. One authority owns subscriptions, settings and live state, so clients never need controller credentials or a second way to change configuration. The external mihomo controller is not linked into the binary.

The kernel identifies each peer; only the recorded owner can connect. A protocol version in the greeting lets a separately updated widget explain an incompatible daemon.
