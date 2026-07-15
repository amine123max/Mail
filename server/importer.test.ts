import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { parseAccountImport } from "./importer";

describe("parseAccountImport", () => {
  it("解析 Tab 分隔账号", () => {
    const result = parseAccountImport("user@example.com\tpass\tclient-id\trefresh-token");
    assert.equal(result.errors.length, 0);
    assert.equal(result.accounts[0].email, "user@example.com");
  });

  it("解析四横线分隔账号并保留令牌中的分隔符", () => {
    const result = parseAccountImport("user@example.com----pass----client-id----token----tail");
    assert.equal(result.errors.length, 0);
    assert.equal(result.accounts[0].refreshToken, "token----tail");
  });

  it("报告无效行号", () => {
    const result = parseAccountImport("bad-line\nuser@example.com----p----c----t");
    assert.equal(result.errors[0].line, 1);
    assert.equal(result.accounts.length, 1);
  });
});
