import { describe, expect, it } from "vitest";
import { EMAIL_RE, safeNext } from "./fields";

describe("safeNext (no open redirects after sign-in)", () => {
  it("keeps same-site paths", () => {
    expect(safeNext("/endpoints")).toBe("/endpoints");
    expect(safeNext("/logs?id=1")).toBe("/logs?id=1");
  });
  it("rejects everything else", () => {
    for (const bad of [null, "", "https://evil.example", "//evil.example", "/\\evil.example", "javascript:alert(1)"]) {
      expect(safeNext(bad)).toBe("/");
    }
  });
  it("validates emails loosely (the API has the final word)", () => {
    expect(EMAIL_RE.test("ana@example.com")).toBe(true);
    expect(EMAIL_RE.test("ana@example")).toBe(false);
  });
});
