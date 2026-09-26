// Bundles harness/app.js (which imports amazon-cognito-identity-js) into a
// single browser-runnable script. amazon-cognito-identity-js pulls in the
// Node "buffer" package for its SRP math, which references the bare `global`
// identifier at module-load time — defining it as `globalThis` here is
// required or the bundle throws a ReferenceError before any of our code runs.
import * as esbuild from "esbuild";

await esbuild.build({
  entryPoints: ["harness/app.js"],
  bundle: true,
  outfile: "harness/dist/bundle.js",
  format: "iife",
  platform: "browser",
  target: "es2020",
  define: { global: "globalThis" },
});
