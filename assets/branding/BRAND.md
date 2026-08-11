# Drenyra Engram — Brand & Banner Regeneration

> **Normative source:** the Drenyra ecosystem brand contract —
> [`drenyra-ai/contracts/brand-system.md`](https://github.com/arkelythex/drenyra-ai/blob/main/contracts/brand-system.md)
> (v0.2 DRAFT) and its canonical tokens at `contracts/brand-system/tokens.json`.
>
> The ecosystem design system is **the same system as Drenyra apps/web**: dark
> + light themes and the cyan/violet accent system (DTCG token pipeline). Engram
> must **not** invent its own palette — in either theme.
>
> **Canonical prompt set:** `docs/assets/brand/gpt-image-prompts.md` (drenyra-ai)
> — the Engram prompt below matches the ecosystem file.

## Current state

| Asset | Status |
| --- | --- |
| `drenyra-engram-banner-1.png` (in README) | **FAILS conformance** — coverage 0.77 < 0.85; off-palette royal blues (`#041c78`-family) and warm beige tones |
| `drenyra-engram-banner-2.png`, `-3.png` | Off-palette variants, unreferenced — delete after regeneration |

## Regeneration prompt (ChatGPT Images 2.0)

Paste this prompt into ChatGPT (Images 2.0 / `gpt-image-1`). The palette is
**mandatory**: only these exact colors may appear; do not let the model invent
the legacy blue `#1a73e8`, off-system cyan `#22d3ee`, purple outside the violet
set, royal blue, or any warm tone.

```text
Design a dark, professional, architectural banner for "Drenyra Engram", an
institutional fiscal-knowledge memory system: scope-first, audit-grade,
agent-agnostic.

Background: deep navy canvas #0B0E11 with a very subtle blueprint grid and
soft radial glows in cyan #3CE6D8 and violet #9B7FE8 at low intensity.

Accent colors ONLY: cyan #3CE6D8 (and lighter #6AEFE4), violet #9B7FE8 (and
lighter #B8A2F0), success green #4ADE94, muted blue-gray #A8B0BC. All
gradients must blend exclusively between these colors — no other hues, no
white glows, no warm tones, no blue #1a73e8.

Visual motif: an abstract memory lattice / knowledge-graph node field
interleaved with a fiscal-scope marker (a subtle document/receipt silhouette
with a verified checkmark). Geometric, architectural, precise — the visual
language of a verifiable accounting system. No cartoon, no mascot, no organic
texture.

NO TEXT of any kind in the image — no titles, no words, no letters. The
message is carried by the README alt text, never by the raster.

Aspect ratio exactly 1400:460. Keep C2PA provenance metadata and the
imperceptible watermark enabled.
```

The raster defaults to the **dark theme surface** (per the brand contract). A
light-theme variant is optional and must follow the light token set exactly
(`canvas #FAFAF9`, `text-primary #16181B`, cyan `#2ECFC2`, violet `#6B54A8`,
success `#1A8F52`).

## Validate

Run the ecosystem checker against the generated file (it also scans
drenyra-ai's own banner; your file is an extra path):

```bash
node /home/dreamcoder08/Documents/PROYECTOS/drenyra-ai/scripts/brand-conformance.mjs \
  assets/branding/drenyra-engram-banner-NEW.png
```

**Acceptance criteria:**

1. The checker reports `✓ <file> (coverage ≥ 0.85)` and the final line is `PASS`.
2. The image contains **no text** (verify visually).
3. The image carries **C2PA provenance metadata** (ChatGPT Images exports it).

**If it fails:** the checker prints the top off-palette colors and their sample
counts, e.g. `coverage 0.77 < 0.85 · off-palette: #e2d2b9 x2 ...`. Feed that
back into the prompt: explicitly ban those hues ("no #e2d2b9 or similar warm
beige tones") and regenerate. Iterate until PASS — usually 1–2 rounds.

## Replace

1. Save the passing file as `assets/branding/drenyra-engram-banner.png`
   (canonical name).
2. Update `README.md`:

   ```markdown
   <img width="1200" alt="Drenyra Engram — One brain for fiscal knowledge. Scope-first, audit-grade, agent-agnostic." src="assets/branding/drenyra-engram-banner.png" />
   ```

3. Delete the three off-palette variants (`banner-1/2/3.png`).
4. Commit with a conventional message, e.g.
   `feat(branding): on-palette banner per brand-system v0.2`.

## Freeze gate

`brand-system` freezes to v0.3 only when every consuming repo (Engram, Drenyra
Pi, Guardian Angel) passes the same checker on its brand assets in both
themes. This file is Engram's side of that gate.
