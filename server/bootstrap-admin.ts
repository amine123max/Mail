import "dotenv/config";
import { readFileSync } from "node:fs";
import { bootstrapAdministrator } from "./auth";
import { closeDatabase } from "./database";

interface BootstrapInput {
  username?: string;
  email?: string;
  password?: string;
}

try {
  const input = JSON.parse(readFileSync(0, "utf8")) as BootstrapInput;
  const administrator = bootstrapAdministrator(
    input.username || "",
    input.email || "",
    input.password || "",
  );
  console.log(`管理员 ${administrator.username} 已创建`);
} catch (error) {
  console.error(error instanceof Error ? error.message : "管理员创建失败");
  process.exitCode = 1;
} finally {
  closeDatabase();
}
