# Omihomo

An Omarchy 4 plugin that manages a [mihomo](https://wiki.metacubex.one) proxy core from the
Omarchy bar, backed by a small bash CLI. This file is the glossary; it is not a spec.

## Language

**Subscription**:
A named remote URL that returns one mihomo YAML file. Exactly one YAML per subscription.
_Avoid_: Profile, feed, source

**Active subscription**:
The one subscription whose YAML is currently loaded into the running mihomo core. There is
never more than one.
_Avoid_: Current profile, selected config

**Group**:
A policy group declared inside a subscription's YAML. Holds configs and has exactly one of
them selected at a time.
_Avoid_: Proxy group, selector

**Config**:
A single proxy entry: one protocol, one address, one port, one set of credentials. A server
can host many configs on different ports, which is why "node" is the wrong word for it.
_Avoid_: Node, profile, proxy, server

**Override layer**:
A user-owned file of edits merged onto a subscription's fetched YAML to produce the YAML
mihomo actually runs. Survives subscription updates because the fetched YAML is only ever a
cache.
_Avoid_: Patch, local config, customisation

**Core**:
The mihomo binary itself, installed from AUR and run as a user-level systemd service.
_Avoid_: Kernel, engine, backend

## Retired terms

**Profile** is deliberately absent. Upstream mihomo, Clash Verge, and Koala Clash all use it
to mean the YAML file, while in conversation it tends to mean a single proxy entry. It is the
most overloaded word in this ecosystem, so Omihomo does not use it at all. The YAML file has
no user-facing name: it is an implementation detail of a Subscription.
