import { z } from "zod";
import type { ImportedAccount } from "./types";

const emailSchema = z.string().trim().email();

export interface ImportParseResult {
  accounts: ImportedAccount[];
  errors: Array<{ line: number; message: string }>;
}

export function parseAccountImport(raw: string): ImportParseResult {
  const accounts: ImportedAccount[] = [];
  const errors: Array<{ line: number; message: string }> = [];

  raw.replace(/^\uFEFF/, "")
    .split(/\r?\n/)
    .forEach((originalLine, index) => {
      const lineNumber = index + 1;
      const line = originalLine.trim();
      if (!line || line.startsWith("#")) return;

      const parts = line.includes("\t")
        ? line.split(/\t+/)
        : line.split("----");

      if (parts.length < 4) {
        errors.push({
          line: lineNumber,
          message: "必须包含邮箱、密码、Client ID、Refresh Token 四个字段",
        });
        return;
      }

      const email = parts[0].trim();
      const password = parts[1].trim();
      const clientId = parts[2].trim();
      const refreshToken = parts.slice(3).join(line.includes("\t") ? "\t" : "----").trim();

      if (!emailSchema.safeParse(email).success) {
        errors.push({ line: lineNumber, message: "邮箱地址格式无效" });
        return;
      }
      if (!password || !clientId || !refreshToken) {
        errors.push({ line: lineNumber, message: "四个字段都不能为空" });
        return;
      }

      accounts.push({ email, password, clientId, refreshToken });
    });

  return { accounts, errors };
}
