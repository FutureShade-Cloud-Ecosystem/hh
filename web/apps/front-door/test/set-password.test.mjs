// The set password page's pure parts: the link it reads, the query it takes
// back out of the address bar, the password rules it checks before it asks,
// and the words every answer becomes. The words are asserted here as text,
// because the screen's words are the contract (the core never explains a
// refusal).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CONSOLE_PASSWORD_WORDS,
  PASSWORD_MIN_LENGTH,
  RECOVERY_DONE_WORDS,
  RECOVERY_RULE_WORD,
  RECOVERY_WORDS,
  SET_PASSWORD_PATH,
  SetPasswordPage,
  confirmMatches,
  isSetPasswordPath,
  passwordAcceptable,
  readRecoveryLink,
  recoveryNotice,
  scrubRecoveryQuery,
} from "../src/set-password.js";
import { SIGN_IN_WORDS } from "../src/signin.js";

test("the page lives at /set-password and answers to it", () => {
  assert.equal(SET_PASSWORD_PATH, "/set-password");
  assert.equal(isSetPasswordPath("/set-password"), true);
  assert.equal(isSetPasswordPath("/set-password/"), true);
  assert.equal(isSetPasswordPath("/SET-PASSWORD"), false);
  assert.equal(isSetPasswordPath("/"), false);
  assert.equal(isSetPasswordPath("/signin"), false);
});

test("the link is read only when both the user id and the secret are there", () => {
  const link = readRecoveryLink("?userId=user-42&secret=abc123&expire=1234");
  assert.deepEqual(link, { userId: "user-42", secret: "abc123" });
  assert.equal(readRecoveryLink("?userId=user-42"), null);
  assert.equal(readRecoveryLink("?secret=abc123"), null);
  assert.equal(readRecoveryLink(""), null);
  assert.equal(readRecoveryLink("?userId=&secret=abc123"), null);
  assert.equal(readRecoveryLink("?userId=user-42&secret="), null);
});

test("the secret is taken out of the address bar in place", () => {
  const calls = [];
  const fake = {
    location: { pathname: "/set-password", search: "?userId=user-42&secret=abc123" },
    history: {
      replaceState: (state, title, url) => calls.push({ state, title, url }),
    },
  };
  scrubRecoveryQuery(fake);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/set-password");
  assert.ok(!calls[0].url.includes("secret"), "the secret is not left in the url");
  assert.ok(!calls[0].url.includes("userId"), "the query is gone");
});

test("the secret is taken out even when the query carries something else too", () => {
  const calls = [];
  const fake = {
    location: { pathname: "/set-password", search: "?userId=u&secret=s&utm=1" },
    history: { replaceState: (state, title, url) => calls.push(url) },
  };
  scrubRecoveryQuery(fake);
  assert.equal(calls[0], "/set-password");
});

test("a password needs eight characters at least", () => {
  assert.equal(PASSWORD_MIN_LENGTH, 8);
  assert.equal(passwordAcceptable("1234567"), false);
  assert.equal(passwordAcceptable("12345678"), true);
  assert.equal(passwordAcceptable("a longer pass phrase"), true);
  assert.equal(passwordAcceptable(""), false);
});

test("the two passwords have to match before anything is sent", () => {
  assert.equal(confirmMatches("same-password", "same-password"), true);
  assert.equal(confirmMatches("same-password", "other-password"), false);
  assert.equal(confirmMatches("same-password", ""), false);
  assert.equal(confirmMatches("", ""), true);
});

test("the console's own rule is said in plain words", () => {
  assert.equal(
    recoveryNotice("password_refused", "user_password_pwned"),
    "That password has been used in a known data breach. Choose a different one.",
  );
  assert.equal(
    recoveryNotice("password_refused", "user_password_recently_used"),
    "Choose a password you have not used here before.",
  );
  assert.equal(
    recoveryNotice("password_refused", "user_password_personal_data"),
    "Do not use your name, email or user ID in the password.",
  );
  assert.equal(recoveryNotice("password_refused", "user_password_mismatch"), RECOVERY_WORDS["mismatch"]);
  assert.equal(
    recoveryNotice("password_refused", "user_password_invalid"),
    "Use at least 8 characters.",
  );
});

test("an expired link says so and asks for a new one", () => {
  assert.equal(
    recoveryNotice("expired", "user_invalid_token"),
    "This link has expired or was already used. Ask for a new one.",
  );
});

test("a network failure uses the sign in screen's own words", () => {
  assert.equal(recoveryNotice("offline", null), SIGN_IN_WORDS["offline"]);
  assert.equal(recoveryNotice("offline", null), "You are offline. Connect to the internet, then try again.");
});

test("a page with no link in it says what it needs", () => {
  assert.equal(recoveryNotice("no_link", null), "This page needs the link from your email.");
});

test("every refusal has words, and an unknown one is never silent", () => {
  for (const code of ["expired", "password_refused", "rate_limited", "unavailable", "offline", "no_link", "mismatch", "too_short"]) {
    const words = recoveryNotice(code, null);
    assert.equal(typeof words, "string");
    assert.ok(words.length > 0, `${code} says something`);
    assert.ok(!words.includes("!"), `${code} carries no exclamation mark`);
    assert.ok(!/[0-9]{3}/.test(words), `${code} carries no status code`);
  }
  assert.equal(recoveryNotice("something_new", null), RECOVERY_WORDS["unavailable"]);
});

test("the console's password rules are all mapped to words", () => {
  for (const [type, words] of Object.entries(CONSOLE_PASSWORD_WORDS)) {
    assert.equal(typeof words, "string");
    assert.ok(words.length > 0, `${type} says something`);
  }
});

test("the rule is shown on the page, and the success says what the brief says", () => {
  assert.equal(RECOVERY_RULE_WORD, "At least 8 characters.");
  assert.equal(RECOVERY_DONE_WORDS.title, "Your password is set");
  assert.equal(RECOVERY_DONE_WORDS.body, "Sign in with your email and the new password.");
  assert.equal(RECOVERY_DONE_WORDS.cta, "Sign in");
});

test("a refusal marks the field it is about, the way the sign in screen marks the password", async () => {
  const asked = [];
  const page = new SetPasswordPage({
    recovery: {
      completeRecovery: async (userId, secret, password) => {
        asked.push({ userId, secret, password });
        return { ok: false, code: "password_refused", consoleType: "user_password_pwned" };
      },
    },
    link: { userId: "user-42", secret: "abc123" },
    onChange: () => {},
  });

  // Two entries that differ: the confirm is the one at fault, and the console
  // is never asked about it.
  await page.submit("a good password", "a different one");
  assert.equal(page.notice, "The two passwords do not match. Type them again.");
  assert.equal(page.field, "confirm");
  assert.equal(asked.length, 0, "the console was never asked");

  // A password that is too short: the password is the one at fault.
  await page.submit("short", "short");
  assert.equal(page.notice, "Use at least 8 characters.");
  assert.equal(page.field, "password");
  assert.equal(asked.length, 0, "the console was never asked");

  // The console's own refusal about the password marks the password.
  await page.submit("a good password", "a good password");
  assert.equal(page.notice, "That password has been used in a known data breach. Choose a different one.");
  assert.equal(page.field, "password");
  assert.equal(asked.length, 1, "the console was asked once");
});

test("a page with no link asks for one and never asks the console", async () => {
  let asked = 0;
  const page = new SetPasswordPage({
    recovery: {
      completeRecovery: async () => {
        asked += 1;
        return { ok: true };
      },
    },
    link: null,
    onChange: () => {},
  });
  assert.equal(page.hasLink, false);
  assert.equal(page.notice, "This page needs the link from your email.");
  await page.submit("a good password", "a good password");
  assert.equal(asked, 0);
  assert.equal(page.notice, "This page needs the link from your email.");
});
