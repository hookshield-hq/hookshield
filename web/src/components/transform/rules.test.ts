import { describe, expect, it } from "vitest";
import { fromRules, newBody, newHeader, parseValue, toRules } from "./rules";

describe("transform rule drafts", () => {
  it("round-trips every operation", () => {
    const rules = {
      version: 1 as const,
      body: [
        { op: "remove" as const, path: "card" },
        { op: "rename" as const, from: "user_id", to: "customerId" },
        { op: "copy" as const, from: "data.object.id", to: "invoiceId" },
        { op: "set" as const, path: "source", value: "stripe" },
        { op: "set" as const, path: "meta", value: { n: 1 } },
        { op: "set_timestamp" as const, path: "forwardedAt", format: "unix" as const },
        { op: "keep" as const, paths: ["id", "type"] },
      ],
      headers: [
        { op: "remove" as const, name: "Stripe-Signature" },
        { op: "rename" as const, from: "X-A", to: "X-B" },
        { op: "set" as const, name: "X-Source", value: "hookshield" },
      ],
    };
    const d = fromRules(rules);
    expect(toRules(d.body, d.headers)).toEqual(rules);
  });

  it("is null without rules (clears the endpoint's rules)", () => {
    expect(toRules([], [])).toBeNull();
  });

  it("types values: JSON when it parses, text otherwise", () => {
    expect(parseValue("42")).toBe(42);
    expect(parseValue("true")).toBe(true);
    expect(parseValue('{"a":1}')).toEqual({ a: 1 });
    expect(parseValue("prod")).toBe("prod");
    expect(parseValue('"quoted"')).toBe("quoted");
  });

  it("trims paths and splits keep lists", () => {
    const b = { ...newBody("keep"), pathsText: " id, data.object.id\ntype " };
    const h = { ...newHeader("set"), name: " X-Env ", value: "prod" };
    expect(toRules([b], [h])).toEqual({ version: 1, body: [{ op: "keep", paths: ["id", "data.object.id", "type"] }], headers: [{ op: "set", name: "X-Env", value: "prod" }] });
  });
});
