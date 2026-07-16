import type { AccountCredentials } from "./types";

export function serializeAccountExport(accounts: AccountCredentials[]): string {
  return accounts
    .map((account) => [account.email, account.password, account.clientId, account.refreshToken].join("----"))
    .join("\n");
}
