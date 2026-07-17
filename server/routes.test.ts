import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { mailPath, parseMailPath, routeForSegment } from "../src/routes";

describe("Mail browser routes", () => {
  it("maps folders, pages, and dialogs to their UI state", () => {
    assert.deepEqual(routeForSegment("sent"), {
      segment: "sent",
      page: "inbox",
      folder: "sent",
      dialog: null,
      known: true,
    });
    assert.deepEqual(routeForSegment("sendmails"), {
      segment: "sendmails",
      page: "compose",
      folder: null,
      dialog: null,
      known: true,
    });
    assert.equal(routeForSegment("accounts").page, "accounts");
    assert.equal(routeForSegment("import").dialog, "import");
    assert.equal(routeForSegment("oauth").page, "oauth");
    assert.equal(routeForSegment("settings").page, "settings");
    assert.equal(routeForSegment("admin").page, "admin");
    assert.equal(routeForSegment("users").page, "users");
  });

  it("parses routes below the configured deployment base", () => {
    assert.equal(parseMailPath("/mail/sendmails", "/mail").segment, "sendmails");
    assert.equal(parseMailPath("/mail/accounts/", "/mail/").segment, "accounts");
    assert.equal(parseMailPath("/mail/", "/mail").segment, "");
    assert.equal(parseMailPath("/sent", "/").segment, "sent");
  });

  it("normalizes unknown routes and builds clean paths", () => {
    assert.equal(parseMailPath("/mail/not-a-page", "/mail").known, false);
    assert.equal(mailPath("sendmails", "/mail/"), "/mail/sendmails");
    assert.equal(mailPath("", "/mail/"), "/mail/");
    assert.equal(mailPath("accounts", "/"), "/accounts");
  });
});
