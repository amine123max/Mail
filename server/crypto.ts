import {
  createCipheriv,
  createDecipheriv,
  createHmac,
  randomBytes,
} from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const dataDir = resolve(process.env.MAIL_DATA_DIR || "./data");
const keyFile = resolve(dataDir, ".master-key");

function readConfiguredKey(value: string): Buffer {
  const trimmed = value.trim();
  const key = /^[a-f\d]{64}$/i.test(trimmed)
    ? Buffer.from(trimmed, "hex")
    : Buffer.from(trimmed, "base64");

  if (key.length !== 32) {
    throw new Error("MAIL_ENCRYPTION_KEY 必须是 32 字节的 Base64 或 64 位十六进制字符串");
  }

  return key;
}

function loadMasterKey(): Buffer {
  if (process.env.MAIL_ENCRYPTION_KEY?.trim()) {
    return readConfiguredKey(process.env.MAIL_ENCRYPTION_KEY);
  }

  if (process.env.NODE_ENV === "production") {
    throw new Error("生产环境必须通过 MAIL_ENCRYPTION_KEY 提供外部加密密钥");
  }

  mkdirSync(dataDir, { recursive: true });
  if (existsSync(keyFile)) {
    const key = Buffer.from(readFileSync(keyFile, "utf8").trim(), "base64");
    if (key.length !== 32) {
      throw new Error("data/.master-key 内容无效，无法解密已有邮箱账号");
    }
    return key;
  }

  const key = randomBytes(32);
  writeFileSync(keyFile, key.toString("base64"), {
    encoding: "utf8",
    mode: 0o600,
  });
  return key;
}

const masterKey = loadMasterKey();

export function encryptSecret(value: string): string {
  const iv = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", masterKey, iv);
  const encrypted = Buffer.concat([
    cipher.update(value, "utf8"),
    cipher.final(),
  ]);
  const tag = cipher.getAuthTag();
  return ["v1", iv.toString("base64"), tag.toString("base64"), encrypted.toString("base64")].join(":");
}

export function decryptSecret(value: string): string {
  const [version, ivValue, tagValue, encryptedValue] = value.split(":");
  if (version !== "v1" || !ivValue || !tagValue || encryptedValue === undefined) {
    throw new Error("数据库中的加密凭据格式无效");
  }

  const decipher = createDecipheriv(
    "aes-256-gcm",
    masterKey,
    Buffer.from(ivValue, "base64"),
  );
  decipher.setAuthTag(Buffer.from(tagValue, "base64"));
  return Buffer.concat([
    decipher.update(Buffer.from(encryptedValue, "base64")),
    decipher.final(),
  ]).toString("utf8");
}

export function blindIndex(value: string): string {
  return createHmac("sha256", masterKey)
    .update(value.trim().toLowerCase())
    .digest("hex");
}
