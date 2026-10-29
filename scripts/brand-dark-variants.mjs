#!/usr/bin/env node
// Derive the Dreamcoder Dark variant of every light brand SVG.
//
// The light SVGs in assets/branding/ are the single source of truth and use
// only Dreamcoder Light tokens. Each light token maps to the Dreamcoder Dark
// token with the same semantic role, so a dark asset never invents a colour.
// A colour outside the table fails the run instead of passing through.
//
// Usage: node scripts/brand-dark-variants.mjs [--check]
//   --check  exit 1 when a committed *-dark.svg is missing or stale.
// Rasterize afterwards with: rsvg-convert -w 1400 -h 460 <in.svg> -o <out.png>

import { readFileSync, writeFileSync, readdirSync, existsSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const BRANDING = join(dirname(fileURLToPath(import.meta.url)), "..", "assets", "branding");

// light token value -> dark token value (role in parentheses)
const LIGHT_TO_DARK = {
  "#f3eadc": "#000000", // bg
  "#e6d7c4": "#0b0b0b", // bg_soft
  "#fff7ea": "#0d0d0f", // surface0 -> raised surface; also on_accent text
  "#decbb1": "#1f1f1f", // surface1 (emphasised card)
  "#c8ad89": "#2e2e2e", // surface2
  "#100c07": "#f5f5f5", // text_heading
  "#17120d": "#e6e6e6", // text
  "#352e22": "#c7c7c7", // muted
  "#554638": "#a7a7a7", // subtle
  "#725e4c": "#a7a7a7", // comment (dimmest label)
  "#8a7358": "#6b6b6b", // border
  "#66513b": "#767676", // border_ui
  "#3e2f20": "#a7a7a7", // border_hi
  "#824f16": "#a5b4fc", // accent: memory
  "#a7471c": "#fbbf24", // accent_2: pending / attention (kept apart from lavender)
  "#0d4a68": "#7dd3fc", // diagnostic / info: evidence
  "#315b31": "#34d399", // sage / success: approved
  "#57478b": "#d4b5fd", // lavender: human authority
  "#842f24": "#fb8585", // error: non-claims
  "#654300": "#fbbf24", // warning: policy restricts
};

const check = process.argv.includes("--check");
let failed = false;

for (const name of readdirSync(BRANDING).sort()) {
  if (!name.endsWith(".svg") || name.endsWith("-dark.svg")) continue;
  const light = readFileSync(join(BRANDING, name), "utf8");
  const unknown = new Set();
  // Shadows stay black: mapping their ink colour to dark-mode text would turn
  // a drop shadow into a glow, which the brand contract forbids.
  const shadowed = light.replace(/flood-color="#[0-9a-fA-F]{6}"/g, 'flood-color="#000000"');
  const dark = shadowed.replace(/#[0-9a-fA-F]{6}\b/g, (hex) => {
    if (hex === "#000000") return hex;
    const mapped = LIGHT_TO_DARK[hex.toLowerCase()];
    if (!mapped) unknown.add(hex);
    return mapped ?? hex;
  });
  if (unknown.size > 0) {
    console.error(`${name}: colours outside the Dreamcoder Light token table: ${[...unknown].join(", ")}`);
    failed = true;
    continue;
  }
  const out = join(BRANDING, name.replace(/\.svg$/, "-dark.svg"));
  if (check) {
    if (!existsSync(out) || readFileSync(out, "utf8") !== dark) {
      console.error(`${out}: missing or stale; run node scripts/brand-dark-variants.mjs`);
      failed = true;
    }
  } else {
    writeFileSync(out, dark);
    console.log(`wrote ${out}`);
  }
}

process.exit(failed ? 1 : 0);
