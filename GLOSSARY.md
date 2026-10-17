# Omihomo

Omihomo manages proxy subscriptions and traffic from the Omarchy bar.

## Language

**Daemon**:
The persistent authority for an Omihomo installation, its settings, subscriptions and running proxy.
_Avoid_: Backend, core service

**Core**:
The proxy engine the daemon hosts to route traffic.
_Avoid_: Daemon, kernel

**Owner**:
The one local account allowed to control an installation.
_Avoid_: Administrator, shared user

**Settings**:
The owner's supported choices for routing, rules and proxy behavior, preserved across subscription updates.
_Avoid_: Override layer, raw config

**Subscription**:
A named remote source for groups, configs and rules.
_Avoid_: Feed, profile

**Active subscription**:
The one subscription selected for the proxy to use, including while the proxy is stopped.
_Avoid_: Selected config, current profile

**Group**:
A subscription's policy for choosing among configs or other groups.
_Avoid_: Selector, proxy group

**Primary group**:
The group whose selection the panel presents as the current config, and which Omihomo's own routing policies use.
_Avoid_: Main group, default group

**Config**:
One proxy entry with a protocol, address and credentials.
_Avoid_: Node, server, profile
