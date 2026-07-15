import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, it } from "node:test";

const testDir = mkdtempSync(join(tmpdir(), "mail-owner-isolation-"));
process.env.MAIL_DATA_DIR = testDir;

let database: typeof import("./database");
let auth: typeof import("./auth");

before(async () => {
  database = await import("./database");
  auth = await import("./auth");
});

const alpha = {
  email: "alpha@example.invalid",
  password: "alpha-password",
  clientId: "alpha-client-id",
  refreshToken: "alpha-refresh-token-long-enough",
};
const beta = {
  email: "beta@example.invalid",
  password: "beta-password",
  clientId: "beta-client-id",
  refreshToken: "beta-refresh-token-long-enough",
};
const guest = {
  email: "guest@example.invalid",
  password: "guest-password",
  clientId: "guest-client-id",
  refreshToken: "guest-refresh-token-long-enough",
};

describe("first deployment administrator setup", () => {
  it("requires a one-time administrator and permanently closes setup", () => {
    assert.equal(database.isSetupRequired(), true);
    const administrator = database.createAdministrator(
      "owner_admin",
      "scrypt:test:hash",
      "owner@example.invalid",
    );
    assert.equal(administrator.is_admin, 1);
    assert.equal(database.isSetupRequired(), false);
    assert.throws(
      () => database.createAdministrator("second_admin", "scrypt:test:hash", "second@example.invalid"),
      /SETUP_ALREADY_COMPLETED/,
    );
  });
});

describe("registration email verification", () => {
  it("uses a five-minute lifetime and consumes a valid code once", () => {
    assert.equal(auth.verificationLifetimeSeconds, 300);
    const email = "verified@example.invalid";
    database.saveEmailVerification(email, "valid-code-hash", new Date(Date.now() + 300_000));
    assert.equal(database.consumeEmailVerification(email, "valid-code-hash"), "verified");
    assert.equal(database.consumeEmailVerification(email, "valid-code-hash"), "missing");
  });

  it("expires old codes and invalidates a code after five wrong attempts", () => {
    database.saveEmailVerification(
      "expired@example.invalid",
      "expired-code-hash",
      new Date(Date.now() - 1_000),
    );
    assert.equal(
      database.consumeEmailVerification("expired@example.invalid", "expired-code-hash"),
      "expired",
    );

    const email = "attempts@example.invalid";
    database.saveEmailVerification(email, "correct-code-hash", new Date(Date.now() + 300_000));
    for (let attempt = 1; attempt < 5; attempt += 1) {
      assert.equal(database.consumeEmailVerification(email, "wrong-code-hash"), "invalid");
    }
    assert.equal(database.consumeEmailVerification(email, "wrong-code-hash"), "attempts_exceeded");
    assert.equal(database.consumeEmailVerification(email, "correct-code-hash"), "missing");
  });

  it("stores verification and user email addresses only as encrypted data", () => {
    const email = "private-registration@example.invalid";
    database.saveEmailVerification(email, "stored-code-hash", new Date(Date.now() + 300_000));

    const { DatabaseSync } = require("node:sqlite") as typeof import("node:sqlite");
    const rawDatabase = new DatabaseSync(database.databasePath, { readOnly: true });
    const verification = rawDatabase
      .prepare("SELECT email_encrypted, email_hash, code_hash FROM email_verifications WHERE email_hash IS NOT NULL")
      .all() as Array<Record<string, string>>;
    const user = rawDatabase
      .prepare("SELECT email_encrypted, email_hash FROM users WHERE username = ?")
      .get("owner_admin") as Record<string, string>;
    assert.ok(verification.every((row) => row.email_encrypted.startsWith("v1:")));
    assert.ok(verification.every((row) => !Object.values(row).some((value) => value.includes(email))));
    assert.ok(user.email_encrypted.startsWith("v1:"));
    assert.ok(!Object.values(user).some((value) => value.includes("owner@example.invalid")));
    rawDatabase.close();
  });
});

describe("multi-tenant account isolation", () => {
  it("scopes list, update, and delete operations by owner", () => {
    database.importAccounts("user:101", [alpha], "skip");
    database.importAccounts("user:202", [beta], "skip");

    const alphaId = database.listAccounts("user:101")[0].id;
    assert.deepEqual(database.listAccounts("user:101").map((item) => item.email), [alpha.email]);
    assert.deepEqual(database.listAccounts("user:202").map((item) => item.email), [beta.email]);
    assert.equal(database.getAccountCredentials("user:202", alphaId), null);
    assert.equal(database.updateAccount("user:202", alphaId, { remark: "cross-user" }), null);
    assert.equal(database.deleteAccount("user:202", alphaId), false);
  });

  it("moves guest accounts into one user namespace and removes the guest copy", () => {
    database.createGuestSession("guest-session", new Date(Date.now() + 60_000));
    database.importAccounts("guest:guest-session", [guest], "skip");
    const transferred = database.transferGuestAccounts("guest-session", 101);

    assert.equal(transferred, 1);
    assert.equal(database.listAccounts("guest:guest-session").length, 0);
    assert.deepEqual(
      database.listAccounts("user:101").map((item) => item.email).sort(),
      [alpha.email, guest.email].sort(),
    );
  });

  it("stores imported credentials as AES-GCM ciphertext", () => {
    const credentials = database.getAccountCredentials(
      "user:202",
      database.listAccounts("user:202")[0].id,
    );
    assert.equal(credentials?.password, beta.password);

    const { DatabaseSync } = require("node:sqlite") as typeof import("node:sqlite");
    const rawDatabase = new DatabaseSync(database.databasePath, { readOnly: true });
    const raw = rawDatabase
      .prepare("SELECT email_encrypted, email_hash, password_encrypted, client_id_encrypted, refresh_token_encrypted FROM accounts WHERE owner_key = ?")
      .get("user:202") as Record<string, string>;
    assert.ok(raw.email_encrypted.startsWith("v1:"));
    assert.match(raw.email_hash, /^[a-f0-9]{64}$/);
    assert.ok(raw.password_encrypted.startsWith("v1:"));
    assert.ok(raw.client_id_encrypted.startsWith("v1:"));
    assert.ok(raw.refresh_token_encrypted.startsWith("v1:"));
    assert.equal(Object.values(raw).some((value) => value.includes(beta.password) || value.includes(beta.email)), false);
    rawDatabase.close();
  });
});

after(() => {
  database.closeDatabase();
  rmSync(testDir, { recursive: true, force: true });
});
