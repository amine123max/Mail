import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { serializeAccountExport } from "./exporter";

describe("account export", () => {
  it("writes one four-field account per line in Mail import format", () => {
    const content = serializeAccountExport([
      {
        id: 1,
        ownerKey: "user:1",
        email: "first@example.com",
        password: "first-password",
        clientId: "first-client",
        refreshToken: "first-refresh",
        remark: "",
      },
      {
        id: 2,
        ownerKey: "user:1",
        email: "second@example.com",
        password: "second-password",
        clientId: "second-client",
        refreshToken: "second-refresh----tail",
        remark: "",
      },
    ]);
    assert.equal(
      content,
      "first@example.com----first-password----first-client----first-refresh\n"
        + "second@example.com----second-password----second-client----second-refresh----tail",
    );
  });
});
