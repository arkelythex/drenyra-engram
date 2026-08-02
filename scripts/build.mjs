/**
 * drenyra-engram build script.
 *
 * 1. Compiles the TypeScript sources to dist/ (ESM, NodeNext) with declarations.
 * No CLI is shipped, so no shebang patching is needed.
 *
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; exit/status codes are JSON integers, never
 * floats.
 */

import { execSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));

console.log("build: compiling with tsc -p tsconfig.build.json");
execSync("bunx tsc -p tsconfig.build.json", { cwd: root, stdio: "inherit" });

console.log("build: done");
