import { test, expect } from "@playwright/test";
import fs from "node:fs";

const ENDPOINT = process.env.KUMOLO_ENDPOINT!;
const POOL_ID = process.env.KUMOLO_POOL_ID!;
const CLIENT_ID = process.env.KUMOLO_CLIENT_ID!;
const LOG_FILE = process.env.KUMOLO_LOG_FILE!;

// kumolo has no email/SMS provider; it logs the confirmation code instead.
function readConfirmationCode(username: string): string {
  const log = fs.readFileSync(LOG_FILE, "utf8");
  const match = log
    .split("\n")
    .filter((line) => line.includes("SignUp confirmation code") && line.includes(username))
    .at(-1)
    ?.match(/code=(\d+)/);
  if (!match) {
    throw new Error(`confirmation code not found in kumolo log for ${username}`);
  }
  return match[1];
}

test("browser SDK SignUp -> ConfirmSignUp -> InitiateAuth succeeds across origins", async ({
  page,
}) => {
  const username = `e2e-browser-${Date.now()}@example.com`;
  const password = "Password1!";

  const url =
    `/index.html?poolId=${encodeURIComponent(POOL_ID)}` +
    `&clientId=${encodeURIComponent(CLIENT_ID)}` +
    `&endpoint=${encodeURIComponent(ENDPOINT)}`;
  await page.goto(url);

  const signUpResult = await page.evaluate(
    ([u, p, e]) => window.kumoloSignUp(u, p, e),
    [username, password, username],
  );
  expect(signUpResult.ok, `SignUp failed: ${signUpResult.code} ${signUpResult.message}`).toBe(
    true,
  );

  const code = readConfirmationCode(username);

  const confirmResult = await page.evaluate(
    ([u, c]) => window.kumoloConfirmSignUp(u, c),
    [username, code],
  );
  expect(
    confirmResult.ok,
    `ConfirmSignUp failed: ${confirmResult.code} ${confirmResult.message}`,
  ).toBe(true);

  const loginResult = await page.evaluate(
    ([u, p]) => window.kumoloLogin(u, p),
    [username, password],
  );
  expect(loginResult.ok, `InitiateAuth failed: ${loginResult.code} ${loginResult.message}`).toBe(
    true,
  );
  expect(loginResult.hasAccessToken).toBe(true);
});
