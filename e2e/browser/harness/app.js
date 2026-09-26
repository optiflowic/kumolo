import { Buffer } from "buffer";
window.Buffer = window.Buffer || Buffer;

import {
  CognitoUserPool,
  CognitoUser,
  AuthenticationDetails,
  CognitoUserAttribute,
} from "amazon-cognito-identity-js";

// Pool/client/endpoint are passed as query params by the Playwright test so
// a single bundle works against whatever kumolo instance/pool it starts.
function pool() {
  const params = new URLSearchParams(window.location.search);
  return new CognitoUserPool({
    UserPoolId: params.get("poolId"),
    ClientId: params.get("clientId"),
    endpoint: params.get("endpoint"),
  });
}

// authenticateUser() defaults to USER_SRP_AUTH, matching real Cognito JS SDK
// usage (and kumolo's own SRP implementation) rather than USER_PASSWORD_AUTH.
window.kumoloSignUp = (username, password, email) =>
  new Promise((resolve) => {
    const attrs = [new CognitoUserAttribute({ Name: "email", Value: email })];
    pool().signUp(username, password, attrs, [], (err) => {
      resolve(err ? { ok: false, code: err.code, message: err.message } : { ok: true });
    });
  });

window.kumoloConfirmSignUp = (username, code) =>
  new Promise((resolve) => {
    const cognitoUser = new CognitoUser({ Username: username, Pool: pool() });
    cognitoUser.confirmRegistration(code, true, (err) => {
      resolve(err ? { ok: false, code: err.code, message: err.message } : { ok: true });
    });
  });

window.kumoloLogin = (username, password) =>
  new Promise((resolve) => {
    const cognitoUser = new CognitoUser({ Username: username, Pool: pool() });
    const authDetails = new AuthenticationDetails({ Username: username, Password: password });
    cognitoUser.authenticateUser(authDetails, {
      onSuccess: (session) =>
        resolve({ ok: true, hasAccessToken: !!session.getAccessToken().getJwtToken() }),
      onFailure: (err) => resolve({ ok: false, code: err.code, message: err.message }),
    });
  });
