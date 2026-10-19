import { copyFile, cp, mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";

// Bundle worker/fonts with the application; PDF previews never depend on a CDN.
const require = createRequire(import.meta.url);
const root = path.dirname(require.resolve("pdfjs-dist/package.json"));
const target = path.resolve("public/pdfjs");
await mkdir(target, { recursive: true });
await copyFile(path.join(root, "build/pdf.worker.min.mjs"), path.join(target, "pdf.worker.min.mjs"));
for (const name of ["cmaps", "standard_fonts", "wasm"]) await cp(path.join(root, name), path.join(target, name), { recursive: true });
