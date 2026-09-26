import { defineConfig } from "@playwright/test";

// The harness must be served from a different origin than kumolo itself —
// same-port would make every request same-origin and CORS would never be
// exercised. run.sh picks kumolo's port and passes harness port + 1 here.
const harnessPort = Number(process.env.HARNESS_PORT || 4173);

export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: `http://localhost:${harnessPort}`,
  },
  webServer: {
    command: `node serve-harness.mjs ${harnessPort}`,
    port: harnessPort,
    reuseExistingServer: false,
    timeout: 10_000,
  },
});
