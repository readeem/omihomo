# Mihomo proxy-provider config extraction research (ticket #4)

Date: 2026-08-18  
Question: can Omihomo reliably recover individual configs from proxy-provider subscriptions by
looking at mihomo's cache and REST API?

## Findings

### Cache path and format

Each `proxy-providers` entry has a required provider `name`, a `type` (`http`, `file`, or
`inline`), and (for file-backed providers) a configurable `path`. The documented example
uses `./proxy_providers/provider1.yaml`; the path is resolved relative to mihomo's home
directory and is subject to the core's safe-path restrictions. The provider data itself is
the provider's proxy YAML (normally a `proxies:` list), not a separately specified,
versioned database format. Sources: [proxy-provider configuration](https://wiki.metacubex.one/config/proxy-providers/).

There is therefore no stable, globally discoverable cache directory that Omihomo can
assume. The configured path is part of the user's mihomo configuration, and an HTTP
provider may be refreshed or rewritten by the core. Treat the file as an implementation
cache, not as a durable application contract. The wiki documents the path and its safety
constraints, but does not promise schema/version stability for consumers.

### Mapping `/proxies` names to provider entries

`GET /proxies` is a runtime view keyed by proxy/group names. It is useful for status and
selection, but it is not a provenance index: names can be transformed by provider
`override` rules (including `additional-prefix`, `additional-suffix`, and `proxy-name`),
and multiple providers can contain equal names. Groups also appear in the runtime proxy
namespace. Consequently, a name returned by `/proxies` cannot be mapped reliably to one
cached entry across multiple providers without retaining the provider configuration and
reimplementing mihomo's transforms and collision behavior. Sources: [proxy-provider
configuration and overrides](https://wiki.metacubex.one/config/proxy-providers/),
[GET /proxies](https://wiki.metacubex.one/api/paths/proxies/).

The practical mapping is only best effort: identify a provider from the loaded YAML,
read that provider's configured file, then match the post-override name while detecting
duplicates. This is inherently vulnerable to updates, renames, collisions, and providers
whose source is not a readable local file.

### `GET /providers/proxies/{name}`

The provider-specific endpoint is more useful than `/proxies` for enumeration: it addresses
a named proxy provider and returns that provider's proxy collection, rather than the
flattened runtime namespace. It still does not expose the original subscription URL,
fetch response, cache filename, or a stable source identifier for each proxy. It also
does not remove the need to handle provider overrides and duplicate names. Source: [mihomo
REST API provider proxy path](https://wiki.metacubex.one/api/paths/providers/).

### Readability and permissions

The cache file is ordinary YAML on disk when the provider is file-backed, so the core's
user can generally read it if filesystem permissions allow. The documented safe-path rule
restricts where mihomo itself may access provider files; it is not an API guarantee that
all provider data will be materialized locally or readable by another user. HTTP-only or
inline providers may have no useful user-readable cache file at all. Source: [provider
`path` and safe-path documentation](https://wiki.metacubex.one/config/proxy-providers/).

## Verdict

Full, reliable extraction is difficult and should not be treated as a core contract. A
best-effort inspector can work for the active configuration when it has the provider
definitions, can read their configured files, and uses `/providers/proxies/{name}` to
enumerate provider-local entries. It cannot promise stable identity or provenance across
multiple providers, subscription refreshes, renamed/overridden entries, HTTP-only
providers, or mihomo versions. Omihomo should model subscription YAML and its own override
layer as the source of truth, using mihomo endpoints for runtime state rather than reverse
engineering cache files.
