import { Buffer } from "buffer";
window.Buffer = window.Buffer || Buffer;

import {
  CognitoUserPool,
  CognitoUser,
  AuthenticationDetails,
  CognitoUserAttribute,
} from "amazon-cognito-identity-js";

interface KumoloResult {
  ok: boolean;
  code?: string;
  message?: string;
  hasAccessToken?: boolean;
}

declare global {
  interface Window {
    Buffer: typeof Buffer;
    kumoloSignUp: (username: string, password: string, email: string) => Promise<KumoloResult>;
    kumoloConfirmSignUp: (username: string, code: string) => Promise<KumoloResult>;
    kumoloLogin: (username: string, password: string) => Promise<KumoloResult>;
  }
}

function pool(): CognitoUserPool {
  const params = new URLSearchParams(window.location.search);
  return new CognitoUserPool({
    UserPoolId: params.get("poolId") ?? "",
    ClientId: params.get("clientId") ?? "",
    endpoint: params.get("endpoint") ?? undefined,
  });
}

window.kumoloSignUp = (username, password, email) =>
  new Promise((resolve) => {
    const attrs = [new CognitoUserAttribute({ Name: "email", Value: email })];
    pool().signUp(username, password, attrs, [], (err) => {
      resolve(err ? { ok: false, code: err.name, message: err.message } : { ok: true });
    });
  });

window.kumoloConfirmSignUp = (username, code) =>
  new Promise((resolve) => {
    const cognitoUser = new CognitoUser({ Username: username, Pool: pool() });
    cognitoUser.confirmRegistration(code, true, (err) => {
      resolve(err ? { ok: false, code: err.name, message: err.message } : { ok: true });
    });
  });

// authenticateUser() defaults to USER_SRP_AUTH.
window.kumoloLogin = (username, password) =>
  new Promise((resolve) => {
    const cognitoUser = new CognitoUser({ Username: username, Pool: pool() });
    const authDetails = new AuthenticationDetails({ Username: username, Password: password });
    cognitoUser.authenticateUser(authDetails, {
      onSuccess: (session) =>
        resolve({ ok: true, hasAccessToken: !!session.getAccessToken().getJwtToken() }),
      onFailure: (err) => resolve({ ok: false, code: err.name, message: err.message }),
    });
  });
