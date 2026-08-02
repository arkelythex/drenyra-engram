/**
 * drenyra-engram package verification — checks the built dist/ tree and the
 * packaged manifest BEFORE the artifact is published. Fails (exit 1) on any
 * missing file, so a broken package never reaches npm.
 *
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; exit codes are JSON integers, never floats.
 */

import { accessSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const errors = [];

function check(relativePath) {
  try {
    accessSync(join(root, relativePath));
  } catch {
    errors.push(`missing: ${relativePath}`);
  }
}

// Library entry: JS + declarations.
check("dist/index.js");
check("dist/index.d.ts");

// Submodule entries (barrel files): JS + declarations.
for (const module of ["core", "store", "search", "lifecycle", "authority"]) {
  check(`dist/${module}/index.js`);
  check(`dist/${module}/index.d.ts`);
}

// Contracts ship in the package.
for (const contract of [
  "README.md",
  "memory.md",
  "scope.md",
  "lifecycle.md",
  "provenance.md",
]) {
  check(`contracts/${contract}`);
}

if (errors.length > 0) {
  console.error("verify-package-files: FAILED");
  for (const e of errors) console.error(`  - ${e}`);
  process.exit(1);
}
console.log("verify-package-files: OK (dist tree + packaged files complete)");
