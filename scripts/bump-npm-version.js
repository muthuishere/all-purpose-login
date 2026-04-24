#!/usr/bin/env node
// Synchronize the `version` field (and the main package's optionalDependencies
// pins) across all 6 npm package.json files to match the given version.
//
// Usage:
//   node scripts/bump-npm-version.js <version>
//
// <version> must be a bare semver (no leading "v").

const fs = require("fs");
const path = require("path");

const SUB_PACKAGES = [
  "darwin-arm64",
  "darwin-x64",
  "linux-arm64",
  "linux-x64",
  "windows-x64",
];

function main() {
  const version = process.argv[2];
  if (!version) {
    console.error("usage: bump-npm-version.js <version>");
    process.exit(2);
  }
  if (!/^\d+\.\d+\.\d+(?:[-+].+)?$/.test(version)) {
    console.error(`invalid version: ${version} (expected semver, no leading 'v')`);
    process.exit(2);
  }

  const repoRoot = path.resolve(__dirname, "..");
  const npmRoot = path.join(repoRoot, "npm");

  // Update main package
  const mainPath = path.join(npmRoot, "package.json");
  const main = JSON.parse(fs.readFileSync(mainPath, "utf8"));
  main.version = version;
  main.optionalDependencies = main.optionalDependencies || {};
  for (const plat of SUB_PACKAGES) {
    main.optionalDependencies[`@muthuishere/apl-${plat}`] = version;
  }
  fs.writeFileSync(mainPath, JSON.stringify(main, null, 2) + "\n");
  console.log(`bumped ${mainPath} -> ${version}`);

  // Update each sub-package
  for (const plat of SUB_PACKAGES) {
    const p = path.join(npmRoot, "platforms", plat, "package.json");
    const pkg = JSON.parse(fs.readFileSync(p, "utf8"));
    pkg.version = version;
    fs.writeFileSync(p, JSON.stringify(pkg, null, 2) + "\n");
    console.log(`bumped ${p} -> ${version}`);
  }
}

main();
