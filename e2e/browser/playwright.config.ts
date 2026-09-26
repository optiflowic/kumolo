import { defineConfig } from "@playwright/test";

// Must differ from kumolo's port, or the CORS this test exists to check for never triggers.
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
    command: `npx tsx serve-harness.ts ${harnessPort}`,
    port: harnessPort,
    reuseExistingServer: false,
    timeout: 10_000,
  },
});
