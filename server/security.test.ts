import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, it } from "node:test";

const testDir = mkdtempSync(join(tmpdir(), "mail-owner-isolation-"));
process.env.MAIL_DATA_DIR = testDir;

let database: typeof import("./database");
let auth: typeof import("./auth");
let outlook: typeof import("./outlook");

before(async () => {
  database = await import("./database");
  auth = await import("./auth");
  outlook = await import("./outlook");
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
const gamma = {
  email: "gamma@example.invalid",
  password: "gamma-password",
  clientId: "gamma-client-id",
  refreshToken: "gamma-refresh-token-long-enough",
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
    const administrator = auth.bootstrapAdministrator(
      "owner_admin",
      "owner@example.invalid",
      "owner-password-123",
    );
    assert.equal(administrator.is_admin, 1);
    assert.equal(database.isSetupRequired(), false);
    assert.equal(
      auth.authenticate("owner@example.invalid", "owner-password-123")?.id,
      administrator.id,
    );
    assert.equal(auth.authenticate("owner_admin", "owner-password-123"), null);
    assert.throws(
      () => auth.bootstrapAdministrator("second_admin", "second@example.invalid", "second-password-123"),
      /管理员初始化已完成/,
    );
  });
});

describe("registration email verification", () => {
  it("renders the Mail branded five-minute verification template", () => {
    const message = auth.buildVerificationMessage("754443");
    assert.match(message.subject, /Mail/);
    assert.match(message.html, /cid:mail-brand-logo/);
    assert.match(message.html, /754443/);
    assert.match(message.html, /5 分钟/);
    assert.match(message.html, /text-align:center/);
    assert.doesNotMatch(message.html, />Mail<\/td>/);
    assert.doesNotMatch(message.html, /如果并非你本人尝试注册或登录 Mail/);
    assert.doesNotMatch(message.html, /安全管理 Outlook 与 Hotmail 邮箱/);
    assert.doesNotMatch(message.html, /OpenAI|ChatGPT/);
  });

  it("localizes the verification email from the preferred browser language", () => {
    assert.equal(auth.resolveVerificationLanguage("en-US,en;q=0.9,zh;q=0.8"), "en");
    assert.equal(auth.resolveVerificationLanguage("fr-FR,zh-CN;q=0.9,en;q=0.8"), "zh");
    assert.equal(auth.resolveVerificationLanguage(undefined), "zh");

    const english = auth.buildVerificationMessage("123456", "en");
    assert.equal(english.subject, "Mail verification code");
    assert.match(english.html, /Enter this temporary verification code to continue/);
    assert.match(english.html, /This code expires in 5 minutes/);
    assert.doesNotMatch(english.html, /输入此临时验证码|安全管理 Outlook/);
  });

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

describe("administrator announcements", () => {
  it("publishes announcements globally and tracks each user's read state", () => {
    const administrator = database.findUserByUsername("owner_admin")!;
    const member = database.createUser(
      "announcement_member",
      "test-password-hash",
      "announcement-member@example.invalid",
    );
    const published = database.createAnnouncement(
      administrator.id,
      "Maintenance",
      "Mail will be updated tonight.",
    );
    assert.equal(published.author, "owner_admin");

    const unread = database.listAnnouncements(member.id);
    assert.equal(unread.unreadCount, 1);
    assert.equal(unread.announcements[0].title, "Maintenance");
    assert.equal(unread.announcements[0].read, false);

    assert.equal(database.markAnnouncementsRead(member.id), 1);
    const read = database.listAnnouncements(member.id);
    assert.equal(read.unreadCount, 0);
    assert.equal(read.announcements[0].read, true);
  });
});

describe("safe original email rendering", () => {
  it("keeps the detailed IMAP response instead of a generic error", () => {
    assert.equal(
      outlook.mailErrorMessage({ responseText: "NO User is authenticated but not connected", message: "Command failed" }),
      "NO User is authenticated but not connected",
    );
  });

  it("embeds CID images and removes unresolved image requests", async () => {
    const inlineImages = new Map([
      ["mail-logo", "data:image/png;base64,cGxhbmU="],
    ]);
    const rendered = await outlook.renderMessageHtml(
      '<table><tr><td><img src="cid:mail-logo" alt="Mail"></td></tr></table><img src="cid:missing">',
      inlineImages,
    );
    assert.match(rendered, /data:image\/png;base64,cGxhbmU=/);
    assert.match(rendered, /mail-image-unavailable/);
    assert.doesNotMatch(rendered, /cid:missing/);
  });

  it("rejects private-network image URLs before fetching", async () => {
    await assert.rejects(() => outlook.validateRemoteImageUrl("http://127.0.0.1/image.png"), /Private/);
    await assert.rejects(() => outlook.validateRemoteImageUrl("http://[::1]/image.png"), /Private/);
    await assert.rejects(() => outlook.validateRemoteImageUrl("http://localhost/image.png"), /Private/);
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

  it("persists account order and never exports another owner's credentials", () => {
    database.importAccounts("user:202", [gamma], "skip");
    const current = database.listAccounts("user:202");
    const reversedIds = current.map((account) => account.id).reverse();
    assert.equal(database.reorderAccounts("user:202", reversedIds), true);
    assert.deepEqual(database.listAccounts("user:202").map((account) => account.id), reversedIds);

    const alphaId = database.listAccounts("user:101").find((account) => account.email === alpha.email)!.id;
    assert.equal(database.reorderAccounts("user:202", [...reversedIds, alphaId]), false);
    assert.deepEqual(
      database.getAccountCredentialsBatch("user:202", [...reversedIds, alphaId]).map((account) => account.email),
      database.listAccounts("user:202").map((account) => account.email),
    );
    assert.equal(database.getAccountCredentialsBatch("user:202", [alphaId]).length, 0);
  });

  it("scopes batch grouping and deletion to the current owner", () => {
    const owned = database.listAccounts("user:202");
    const ownedIds = owned.map((account) => account.id);
    const foreignId = database.listAccounts("user:101")[0].id;

    assert.equal(database.setAccountsGroup("user:202", ownedIds, "Work"), true);
    assert.deepEqual(database.listAccounts("user:202").map((account) => account.group), ownedIds.map(() => "Work"));
    assert.equal(database.setAccountsGroup("user:202", [ownedIds[0], foreignId], "Leaked"), false);
    assert.equal(database.listAccounts("user:101")[0].group, "");

    assert.equal(database.deleteAccounts("user:202", [ownedIds[0], foreignId]), null);
    assert.equal(database.listAccounts("user:202").length, ownedIds.length);
    assert.equal(database.deleteAccounts("user:202", [ownedIds[0]]), 1);
    assert.equal(database.listAccounts("user:202").length, ownedIds.length - 1);
  });
});

after(() => {
  database.closeDatabase();
  rmSync(testDir, { recursive: true, force: true });
});
