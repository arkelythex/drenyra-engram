# Drenyra Engram — Brand & Banner

> **Normative source:** the Drenyra ecosystem brand contract —
> [`drenyra-ai/contracts/brand-system.md`](https://github.com/arkelythex/drenyra-ai/blob/main/contracts/brand-system.md)
> (v0.2 DRAFT) and canonical tokens at `contracts/brand-system/tokens.json`.
>
> The ecosystem design system is **the Dreamcoder Workbench canonical tokens**:
> Cocoa/Lúcuma Light (warm ivory `#F3EADC`, dark ink `#17120D`) editorial
> surface and Anthracite Steel dark, with cocoa `#824F16` / terracotta
> `#A7471C` accents — readability before decoration. No product invents its
> own palette.

## Regeneration prompt (ChatGPT Images 2.0)

> **Art direction (v2, Dreamcoder Light + Black Dark OLED):** see
> [gpt-image-prompts.md](https://github.com/arkelythex/drenyra-ai/blob/main/docs/assets/brand/gpt-image-prompts.md).
> Combine the Shared DNA block (section 4) with the product section below; the
> embedded prompt is the product section only and may trail the canonical file.

The canonical set lives in
[`drenyra-ai/docs/assets/brand/gpt-image-prompts.md`](https://github.com/arkelythex/drenyra-ai/blob/main/docs/assets/brand/gpt-image-prompts.md).
The Drenyra Engram prompt is the **institutional archive** motif:

```text
Subject: institutional fiscal memory as a living institutional archive. The hero on the right third is a warm ivory archive expediente (surface #FFF7EA on #F3EADC paper) with visible structure: an effectiveAt / recordedAt header in dark ink #17120D, a confidence gauge, an approved-state seal with a sage #315B31 check, an evidence strip, a vigent-rule citation in cocoa #824F16, and a verifiable archive seal. Beneath it, a thin printed timeline reconstructs the chain: fact → rule → adjustment → evidence → late exception → receipt — like a precision engineering schematic on warm technical paper.

Composition: editorial, audit-grade, calm. One strong archive object, generous negative space, hairline rules — no network graph, no node-and-edge abstraction, no glow. The object reads as an institutional file that can rebuild its context without the original agent: supersede, never mutate.

Signature detail: the verifiable seal engraving and the printed reconstructible timeline resolving under it.
```

## Validate

```bash
node ../drenyra-ai/scripts/brand-conformance.mjs \
  assets/branding/drenyra-engram-banner.png
# expect: ✓ <file> (coverage >= 0.92) ... PASS
```

The checker is referenced from the sibling-checkout layout: clone `drenyra-ai`
next to this repository so `../drenyra-ai/scripts/brand-conformance.mjs`
resolves (the same `../<repo>` layout `drenyra-ai/scripts/brand-ecosystem-status.mjs`
assumes) — no host-specific absolute path.

Iterate with the checker's off-palette feedback until coverage ≥ 0.92. Then
`bun run brand:ecosystem` in drenyra-ai must report this repo `PASS` before
brand-system can freeze to v0.3.

## Freeze gate

`brand-system` freezes to v0.3 only when every consuming repo (App Web, Pi,
Engram, Skills, Guardian Angel) passes the same checker on its brand assets in
both themes.
