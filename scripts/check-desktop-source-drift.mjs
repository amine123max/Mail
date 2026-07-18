import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const webRoot = resolve(root, "src");
const desktopRoot = resolve(root, "desktop-client/apps/src");
const platformAdapters = new Set([
  "api.ts",
  "App.tsx",
  "i18n.tsx",
  "main.tsx",
  "styles.css",
]);

if (!existsSync(desktopRoot)) {
  console.log("Desktop source drift check skipped because desktop-client is not present in this checkout.");
  process.exit(0);
}

const relativeFiles = (directory) => {
  const visit = (current) => readdirSync(current, { withFileTypes: true }).flatMap((entry) => {
    const path = resolve(current, entry.name);
    return entry.isDirectory() ? visit(path) : [relative(directory, path).replaceAll("\\", "/")];
  });
  return visit(directory);
};

const digest = (path) => createHash("sha256")
  .update(readFileSync(path, "utf8").replace(/\r\n/g, "\n"))
  .digest("hex");

const webFiles = new Set(relativeFiles(webRoot));
const desktopFiles = new Set(relativeFiles(desktopRoot));
const sharedFiles = [...webFiles].filter((path) => desktopFiles.has(path) && !platformAdapters.has(path)).sort();
const drifted = sharedFiles.filter((path) => digest(resolve(webRoot, path)) !== digest(resolve(desktopRoot, path)));

if (drifted.length > 0) {
  console.error("Shared source drift detected. Move the change into packages/mail-ui or update both platform adapters:");
  for (const path of drifted) console.error(`- ${path}`);
  process.exit(1);
}

if (sharedFiles.length === 0) {
  throw new Error("desktop source drift manifest found no guarded shared files");
}

console.log(`Desktop source drift check passed (${sharedFiles.length} guarded files, ${platformAdapters.size} explicit platform adapters).`);
