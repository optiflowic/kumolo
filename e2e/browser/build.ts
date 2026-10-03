// `global` must be defined: amazon-cognito-identity-js's Node "buffer"
// dependency references it at load time and throws in a browser otherwise.
import * as esbuild from "esbuild";

await esbuild.build({
  entryPoints: ["harness/app.ts"],
  bundle: true,
  outfile: "harness/dist/bundle.js",
  format: "iife",
  platform: "browser",
  target: "es2020",
  define: { global: "globalThis" },
});
