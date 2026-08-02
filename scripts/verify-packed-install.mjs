/**
 * drenyra-engram packed-install verification.
 *
 * Proves the PUBLISHED artifact works, not just the source tree:
 *   1. npm pack → produces the .tgz exactly as npm would publish it
 *   2. npm installs the .tgz into a clean temp dir
 *   3. the installed library entry resolves under plain Node and exposes the
 *      public API (scopeFirstSearch + InMemoryMemoryStore)
 *
 * This is the test that catches "source works, packaged artifact broken"
 * regressions (missing files, unresolved imports, wrong export paths).
 *
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; exit codes are JSON integers, never floats.
 */

import { execSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const pkg = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
const tgzName = `drenyra-engram-${pkg.version}.tgz`;
const work = mkdtempSync(join(tmpdir(), "drenyra-engram-pack-"));
const failures = [];

try {
  console.log("pack: npm pack");
  execSync(`npm pack --pack-destination ${work}`, { cwd: root, stdio: "inherit" });

  console.log("install: npm install --no-save the tgz into a clean dir");
  const installDir = mkdtempSync(join(tmpdir(), "drenyra-engram-install-"));
  execSync(
    `npm install --no-save --no-package-lock --prefix ${installDir} ${join(work, tgzName)}`,
    { cwd: root, stdio: "pipe" },
  );

  const libPath = join(installDir, "node_modules", "drenyra-engram", "dist", "index.js");
  try {
    console.log("resolve: library entry under Node");
    const probe = `node -e "import('file://${libPath}').then(m => { if (typeof m.scopeFirstSearch !== 'function' || typeof m.InMemoryMemoryStore !== 'function') process.exit(1); console.log('packed-install: library entry resolves and exposes the public API — OK'); }).catch(e => { console.error(e); process.exit(1); })"`;
    execSync(probe, { cwd: installDir, stdio: "inherit" });
  } catch {
    failures.push("packed library entry did not resolve under Node");
  }
} finally {
  rmSync(work, { recursive: true, force: true });
}

if (failures.length > 0) {
  console.error("verify-packed-install: FAILED");
  for (const f of failures) console.error(`  - ${f}`);
  process.exit(1);
}
console.log("verify-packed-install: OK");
