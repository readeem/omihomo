# Keep hosting admission and lifecycle outside the reverse engine

Hosting uses pinned Xray native VLESS reverse codecs and Reality behind `hosting.Transport`, with Omihomo owning the single connection, direct networking, validated destination mapping, admission budgets and revocation. Mihomo lacks the reverse publisher; the standalone Xray executable's automatic worker/retry policy does not provide the required control, so it is not shipped as a helper. Embedding these maintained codecs avoids a new reverse protocol, helper privileges and any service sandbox relaxation, while adding a second network parser to the existing privileged process's trust boundary.

The implementation and [dependency assessment](../research/2026-11-02-hosting-adapter.md) are reviewable together. Dependency inclusion is a proposal for manual review before release or deployment; the existing owner-only control socket remains the only local control surface.
