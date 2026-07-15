import { DatabaseSync } from "node:sqlite";
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { blindIndex, decryptSecret, encryptSecret } from "./crypto";
import type {
  AccountCredentials,
  ImportedAccount,
  PublicAccount,
  StoredAccountRow,
  UserRow,
} from "./types";

const dataDir = resolve(process.env.MAIL_DATA_DIR || "./data");
mkdirSync(dataDir, { recursive: true });

export const databasePath = resolve(dataDir, "mail.sqlite");
const db = new DatabaseSync(databasePath);

db.exec(`
  PRAGMA journal_mode = WAL;
  PRAGMA foreign_keys = ON;
  PRAGMA busy_timeout = 5000;

  CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE COLLATE NOCASE,
    email_encrypted TEXT,
    email_hash TEXT,
    password_hash TEXT NOT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
  );

  CREATE TABLE IF NOT EXISTS guest_sessions (
    id TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
  );

  CREATE TABLE IF NOT EXISTS user_sessions (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
  );

  CREATE TABLE IF NOT EXISTS email_verifications (
    email_hash TEXT PRIMARY KEY,
    email_encrypted TEXT NOT NULL,
    code_hash TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
  );
`);

const userColumns = db.prepare("PRAGMA table_info(users)").all() as Array<{ name: string }>;
if (!userColumns.some((column) => column.name === "email_encrypted")) {
  db.exec("ALTER TABLE users ADD COLUMN email_encrypted TEXT");
}
if (!userColumns.some((column) => column.name === "email_hash")) {
  db.exec("ALTER TABLE users ADD COLUMN email_hash TEXT");
}
if (!userColumns.some((column) => column.name === "is_admin")) {
  db.exec("ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0");
}

const existingUserCount = Number(
  (db.prepare("SELECT COUNT(*) AS count FROM users").get() as { count: number }).count,
);
if (existingUserCount > 0) {
  const administratorCount = Number(
    (db.prepare("SELECT COUNT(*) AS count FROM users WHERE is_admin = 1").get() as { count: number }).count,
  );
  if (administratorCount === 0) {
    db.exec("UPDATE users SET is_admin = 1 WHERE id = (SELECT id FROM users ORDER BY id ASC LIMIT 1)");
  }
}

const accountTable = db
  .prepare("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'accounts'")
  .get() as { name: string } | undefined;

if (!accountTable) {
  createAccountsTable();
} else {
  const columns = db.prepare("PRAGMA table_info(accounts)").all() as Array<{ name: string }>;
  if (!columns.some((column) => column.name === "owner_key")) {
    db.exec("BEGIN IMMEDIATE");
    try {
      db.exec(`
        ALTER TABLE accounts RENAME TO accounts_legacy;
        CREATE TABLE accounts (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          owner_key TEXT NOT NULL,
          email TEXT NOT NULL COLLATE NOCASE,
          password_encrypted TEXT NOT NULL,
          client_id_encrypted TEXT NOT NULL,
          refresh_token_encrypted TEXT NOT NULL,
          remark TEXT NOT NULL DEFAULT '',
          created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
          updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
          last_sync_at TEXT,
          UNIQUE(owner_key, email)
        );
        INSERT INTO accounts (
          id, owner_key, email, password_encrypted, client_id_encrypted,
          refresh_token_encrypted, remark, created_at, updated_at, last_sync_at
        )
        SELECT
          id, 'user:1', email, password_encrypted, client_id_encrypted,
          refresh_token_encrypted, remark, created_at, updated_at, last_sync_at
        FROM accounts_legacy;
        DROP TABLE accounts_legacy;
      `);
      db.exec("COMMIT");
    } catch (error) {
      db.exec("ROLLBACK");
      throw error;
    }
  }
}

const privacyColumns = db.prepare("PRAGMA table_info(accounts)").all() as Array<{ name: string }>;
if (!privacyColumns.some((column) => column.name === "email_hash")) {
  const legacyRows = db.prepare("SELECT * FROM accounts").all() as Array<{
    id: number;
    owner_key: string;
    email: string;
    password_encrypted: string;
    client_id_encrypted: string;
    refresh_token_encrypted: string;
    remark: string;
    created_at: string;
    updated_at: string;
    last_sync_at: string | null;
  }>;
  db.exec("BEGIN IMMEDIATE");
  try {
    db.exec("ALTER TABLE accounts RENAME TO accounts_plain_email");
    createAccountsTable();
    const migrate = db.prepare(`
      INSERT INTO accounts (
        id, owner_key, email_encrypted, email_hash, password_encrypted,
        client_id_encrypted, refresh_token_encrypted, remark,
        created_at, updated_at, last_sync_at
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `);
    for (const row of legacyRows) {
      migrate.run(
        row.id,
        row.owner_key,
        encryptSecret(row.email),
        blindIndex(row.email),
        row.password_encrypted,
        row.client_id_encrypted,
        row.refresh_token_encrypted,
        row.remark,
        row.created_at,
        row.updated_at,
        row.last_sync_at,
      );
    }
    db.exec("DROP TABLE accounts_plain_email");
    db.exec("COMMIT");
  } catch (error) {
    db.exec("ROLLBACK");
    throw error;
  }
}

db.exec(`
  CREATE INDEX IF NOT EXISTS idx_accounts_owner_updated
    ON accounts(owner_key, updated_at DESC);
  CREATE INDEX IF NOT EXISTS idx_guest_sessions_expiry
    ON guest_sessions(expires_at);
  CREATE INDEX IF NOT EXISTS idx_user_sessions_expiry
    ON user_sessions(expires_at);
  CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_hash
    ON users(email_hash) WHERE email_hash IS NOT NULL;
  CREATE INDEX IF NOT EXISTS idx_email_verifications_expiry
    ON email_verifications(expires_at);
`);

function createAccountsTable() {
  db.exec(`
    CREATE TABLE accounts (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      owner_key TEXT NOT NULL,
      email_encrypted TEXT NOT NULL,
      email_hash TEXT NOT NULL,
      password_encrypted TEXT NOT NULL,
      client_id_encrypted TEXT NOT NULL,
      refresh_token_encrypted TEXT NOT NULL,
      remark TEXT NOT NULL DEFAULT '',
      created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
      updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
      last_sync_at TEXT,
      UNIQUE(owner_key, email_hash)
    );
  `);
}

function toPublic(row: StoredAccountRow): PublicAccount {
  return {
    id: row.id,
    email: decryptSecret(row.email_encrypted),
    remark: row.remark,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
    lastSyncAt: row.last_sync_at,
  };
}

function asStored(value: unknown): StoredAccountRow | undefined {
  return value as StoredAccountRow | undefined;
}

export function listAccounts(ownerKey: string): PublicAccount[] {
  const rows = db
    .prepare("SELECT * FROM accounts WHERE owner_key = ? ORDER BY updated_at DESC, id DESC")
    .all(ownerKey) as unknown as StoredAccountRow[];
  return rows.map(toPublic);
}

export function getAccountCredentials(ownerKey: string, id: number): AccountCredentials | null {
  const row = asStored(
    db.prepare("SELECT * FROM accounts WHERE owner_key = ? AND id = ?").get(ownerKey, id),
  );
  if (!row) return null;

  return {
    id: row.id,
    ownerKey: row.owner_key,
    email: decryptSecret(row.email_encrypted),
    password: decryptSecret(row.password_encrypted),
    clientId: decryptSecret(row.client_id_encrypted),
    refreshToken: decryptSecret(row.refresh_token_encrypted),
    remark: row.remark,
  };
}

export function importAccounts(
  ownerKey: string,
  accounts: ImportedAccount[],
  mode: "skip" | "overwrite",
): { inserted: number; updated: number; skipped: number } {
  let inserted = 0;
  let updated = 0;
  let skipped = 0;

  const find = db.prepare("SELECT id FROM accounts WHERE owner_key = ? AND email_hash = ?");
  const insert = db.prepare(`
    INSERT INTO accounts (
      owner_key, email_encrypted, email_hash, password_encrypted, client_id_encrypted,
      refresh_token_encrypted, remark
    ) VALUES (?, ?, ?, ?, ?, ?, ?)
  `);
  const update = db.prepare(`
    UPDATE accounts
      SET email_encrypted = ?, password_encrypted = ?, client_id_encrypted = ?,
          refresh_token_encrypted = ?, remark = ?,
          updated_at = CURRENT_TIMESTAMP
      WHERE owner_key = ? AND email_hash = ?
  `);

  db.exec("BEGIN IMMEDIATE");
  try {
    for (const account of accounts) {
      const emailHash = blindIndex(account.email);
      const existing = find.get(ownerKey, emailHash) as { id: number } | undefined;
      if (existing && mode === "skip") {
        skipped += 1;
        continue;
      }

      const encrypted = {
        email: encryptSecret(account.email),
        password: encryptSecret(account.password),
        clientId: encryptSecret(account.clientId),
        refreshToken: encryptSecret(account.refreshToken),
      };

      if (existing) {
        update.run(
          encrypted.email,
          encrypted.password,
          encrypted.clientId,
          encrypted.refreshToken,
          account.remark || "",
          ownerKey,
          emailHash,
        );
        updated += 1;
      } else {
        insert.run(
          ownerKey,
          encrypted.email,
          emailHash,
          encrypted.password,
          encrypted.clientId,
          encrypted.refreshToken,
          account.remark || "",
        );
        inserted += 1;
      }
    }
    db.exec("COMMIT");
  } catch (error) {
    db.exec("ROLLBACK");
    throw error;
  }

  return { inserted, updated, skipped };
}

export function updateAccount(
  ownerKey: string,
  id: number,
  changes: { remark?: string; refreshToken?: string; lastSync?: boolean },
): PublicAccount | null {
  const row = asStored(
    db.prepare("SELECT * FROM accounts WHERE owner_key = ? AND id = ?").get(ownerKey, id),
  );
  if (!row) return null;

  db.prepare(`
    UPDATE accounts
      SET remark = ?, refresh_token_encrypted = ?,
          last_sync_at = ?, updated_at = CURRENT_TIMESTAMP
      WHERE owner_key = ? AND id = ?
  `).run(
    changes.remark ?? row.remark,
    changes.refreshToken ? encryptSecret(changes.refreshToken) : row.refresh_token_encrypted,
    changes.lastSync ? new Date().toISOString() : row.last_sync_at,
    ownerKey,
    id,
  );

  const updated = asStored(
    db.prepare("SELECT * FROM accounts WHERE owner_key = ? AND id = ?").get(ownerKey, id),
  );
  return updated ? toPublic(updated) : null;
}

export function updateRefreshToken(ownerKey: string, id: number, refreshToken: string): void {
  db.prepare(`
    UPDATE accounts
      SET refresh_token_encrypted = ?, updated_at = CURRENT_TIMESTAMP
      WHERE owner_key = ? AND id = ?
  `).run(encryptSecret(refreshToken), ownerKey, id);
}

export function markAccountSynced(ownerKey: string, id: number): void {
  db.prepare(`
    UPDATE accounts
      SET last_sync_at = ?, updated_at = CURRENT_TIMESTAMP
      WHERE owner_key = ? AND id = ?
  `).run(new Date().toISOString(), ownerKey, id);
}

export function deleteAccount(ownerKey: string, id: number): boolean {
  const result = db.prepare("DELETE FROM accounts WHERE owner_key = ? AND id = ?").run(ownerKey, id);
  return result.changes > 0;
}

export function findUserByUsername(username: string): UserRow | null {
  return (db.prepare("SELECT * FROM users WHERE username = ?").get(username) as UserRow | undefined) || null;
}

export function findUserById(id: number): UserRow | null {
  return (db.prepare("SELECT * FROM users WHERE id = ?").get(id) as UserRow | undefined) || null;
}

export function findUserByEmail(email: string): UserRow | null {
  return (
    (db.prepare("SELECT * FROM users WHERE email_hash = ?").get(blindIndex(email)) as UserRow | undefined) ||
    null
  );
}

export function createUser(
  username: string,
  passwordHash: string,
  email?: string,
  isAdmin = false,
): UserRow {
  const result = db
    .prepare("INSERT INTO users (username, email_encrypted, email_hash, password_hash, is_admin) VALUES (?, ?, ?, ?, ?)")
    .run(
      username,
      email ? encryptSecret(email) : null,
      email ? blindIndex(email) : null,
      passwordHash,
      isAdmin ? 1 : 0,
    );
  return findUserById(Number(result.lastInsertRowid))!;
}

export function isSetupRequired(): boolean {
  const row = db.prepare("SELECT 1 AS present FROM users LIMIT 1").get() as
    | { present: number }
    | undefined;
  return !row;
}

export function createAdministrator(username: string, passwordHash: string, email: string): UserRow {
  db.exec("BEGIN IMMEDIATE");
  try {
    if (!isSetupRequired()) {
      throw new Error("SETUP_ALREADY_COMPLETED");
    }
    const user = createUser(username, passwordHash, email, true);
    db.exec("COMMIT");
    return user;
  } catch (error) {
    db.exec("ROLLBACK");
    throw error;
  }
}

export function saveEmailVerification(email: string, codeHash: string, expiresAt: Date): void {
  db.prepare(`
    INSERT INTO email_verifications (
      email_hash, email_encrypted, code_hash, attempts, created_at, expires_at
    ) VALUES (?, ?, ?, 0, ?, ?)
    ON CONFLICT(email_hash) DO UPDATE SET
      email_encrypted = excluded.email_encrypted,
      code_hash = excluded.code_hash,
      attempts = 0,
      created_at = excluded.created_at,
      expires_at = excluded.expires_at
  `).run(
    blindIndex(email),
    encryptSecret(email),
    codeHash,
    new Date().toISOString(),
    expiresAt.toISOString(),
  );
}

export function canSendEmailVerification(email: string, cooldownSeconds = 60): boolean {
  const row = db
    .prepare("SELECT created_at FROM email_verifications WHERE email_hash = ?")
    .get(blindIndex(email)) as { created_at: string } | undefined;
  return !row || new Date(row.created_at).getTime() <= Date.now() - cooldownSeconds * 1000;
}

export type EmailVerificationResult =
  | "verified"
  | "missing"
  | "expired"
  | "invalid"
  | "attempts_exceeded";

export function consumeEmailVerification(email: string, codeHash: string): EmailVerificationResult {
  const emailHash = blindIndex(email);
  const row = db
    .prepare("SELECT code_hash, attempts, expires_at FROM email_verifications WHERE email_hash = ?")
    .get(emailHash) as { code_hash: string; attempts: number; expires_at: string } | undefined;
  if (!row) return "missing";
  if (new Date(row.expires_at).getTime() <= Date.now()) {
    db.prepare("DELETE FROM email_verifications WHERE email_hash = ?").run(emailHash);
    return "expired";
  }
  if (row.attempts >= 5) {
    db.prepare("DELETE FROM email_verifications WHERE email_hash = ?").run(emailHash);
    return "attempts_exceeded";
  }
  if (row.code_hash !== codeHash) {
    const attempts = row.attempts + 1;
    if (attempts >= 5) {
      db.prepare("DELETE FROM email_verifications WHERE email_hash = ?").run(emailHash);
      return "attempts_exceeded";
    }
    db.prepare("UPDATE email_verifications SET attempts = ? WHERE email_hash = ?").run(attempts, emailHash);
    return "invalid";
  }
  db.prepare("DELETE FROM email_verifications WHERE email_hash = ?").run(emailHash);
  return "verified";
}

export function deleteEmailVerification(email: string): void {
  db.prepare("DELETE FROM email_verifications WHERE email_hash = ?").run(blindIndex(email));
}

export function createGuestSession(id: string, expiresAt: Date): void {
  db.prepare(`
    INSERT OR REPLACE INTO guest_sessions (id, created_at, expires_at)
    VALUES (?, ?, ?)
  `).run(id, new Date().toISOString(), expiresAt.toISOString());
}

export function guestSessionExists(id: string): boolean {
  const row = db.prepare("SELECT expires_at FROM guest_sessions WHERE id = ?").get(id) as { expires_at: string } | undefined;
  return Boolean(row && new Date(row.expires_at).getTime() > Date.now());
}

export function deleteGuestSession(id: string): void {
  db.prepare("DELETE FROM accounts WHERE owner_key = ?").run(`guest:${id}`);
  db.prepare("DELETE FROM guest_sessions WHERE id = ?").run(id);
}

export function transferGuestAccounts(guestId: string, userId: number): number {
  const guestOwner = `guest:${guestId}`;
  const userOwner = `user:${userId}`;
  const count = db.prepare("SELECT COUNT(*) AS count FROM accounts WHERE owner_key = ?").get(guestOwner) as { count: number };
  db.exec("BEGIN IMMEDIATE");
  try {
    db.prepare(`
      INSERT INTO accounts (
        owner_key, email_encrypted, email_hash, password_encrypted, client_id_encrypted,
        refresh_token_encrypted, remark, created_at, updated_at, last_sync_at
      )
      SELECT ?, email_encrypted, email_hash, password_encrypted, client_id_encrypted,
        refresh_token_encrypted, remark, created_at, CURRENT_TIMESTAMP, last_sync_at
      FROM accounts WHERE owner_key = ?
      ON CONFLICT(owner_key, email_hash) DO UPDATE SET
        email_encrypted = excluded.email_encrypted,
        password_encrypted = excluded.password_encrypted,
        client_id_encrypted = excluded.client_id_encrypted,
        refresh_token_encrypted = excluded.refresh_token_encrypted,
        remark = excluded.remark,
        updated_at = CURRENT_TIMESTAMP
    `).run(userOwner, guestOwner);
    db.prepare("DELETE FROM accounts WHERE owner_key = ?").run(guestOwner);
    db.prepare("DELETE FROM guest_sessions WHERE id = ?").run(guestId);
    db.exec("COMMIT");
    return Number(count.count || 0);
  } catch (error) {
    db.exec("ROLLBACK");
    throw error;
  }
}

export function cleanupExpiredGuests(): number {
  const expired = db
    .prepare("SELECT id FROM guest_sessions WHERE expires_at <= ?")
    .all(new Date().toISOString()) as Array<{ id: string }>;
  for (const guest of expired) deleteGuestSession(guest.id);
  return expired.length;
}

export function createUserSession(id: string, userId: number, expiresAt: Date): void {
  db.prepare(`
    INSERT INTO user_sessions (id, user_id, created_at, expires_at)
    VALUES (?, ?, ?, ?)
  `).run(id, userId, new Date().toISOString(), expiresAt.toISOString());
}

export function userSessionExists(id: string, userId: number): boolean {
  const row = db
    .prepare("SELECT expires_at FROM user_sessions WHERE id = ? AND user_id = ?")
    .get(id, userId) as { expires_at: string } | undefined;
  return Boolean(row && new Date(row.expires_at).getTime() > Date.now());
}

export function deleteUserSession(id: string): void {
  db.prepare("DELETE FROM user_sessions WHERE id = ?").run(id);
}

export function cleanupExpiredUserSessions(): number {
  const result = db.prepare("DELETE FROM user_sessions WHERE expires_at <= ?").run(new Date().toISOString());
  return Number(result.changes);
}

export function cleanupExpiredEmailVerifications(): number {
  const result = db
    .prepare("DELETE FROM email_verifications WHERE expires_at <= ? OR attempts >= 5")
    .run(new Date().toISOString());
  return Number(result.changes);
}

export function closeDatabase(): void {
  db.close();
}
