import { describe, expect, it } from "vitest";
import { contentSecurityPolicy } from "./csp";

describe("contentSecurityPolicy", () => {
  it("allows only this origin and the API", () => {
    const csp = contentSecurityPolicy("https://api.example.com/some/path");
    expect(csp).toContain("connect-src 'self' https://api.example.com;");
    expect(csp).toContain("frame-ancestors 'none'");
    expect(csp).toContain("object-src 'none'");
    expect(csp).not.toContain("unsafe-eval");
  });
  it("adds what next dev needs in development", () => {
    const csp = contentSecurityPolicy("http://localhost:8081", true);
    expect(csp).toContain("'unsafe-eval'");
    expect(csp).toContain("connect-src 'self' http://localhost:8081 ws: wss:");
  });
  it("ignores an invalid API URL", () => {
    expect(contentSecurityPolicy("not a url")).toContain("connect-src 'self';");
  });
});
