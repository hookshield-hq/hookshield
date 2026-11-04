import { describe, expect, it } from "vitest";
import { plans } from "./pricing";

// The numbers must also match hookshield-cloud/backend/internal/plans (tested
// there); this only guards the shape the pricing section relies on.
describe("pricing", () => {
  it("lists the four Cloud plans, cheapest first", () => {
    expect(plans.map((p) => p.id)).toEqual(["developer", "pro", "business", "enterprise"]);
    const priced = plans.filter((p) => p.monthlyCents !== null).map((p) => p.monthlyCents as number);
    expect(priced).toEqual([...priced].sort((a, b) => a - b));
  });

  it("marks exactly one plan as popular", () => {
    expect(plans.filter((p) => p.popular)).toHaveLength(1);
  });

  it("only the free plan stops at its limit", () => {
    expect(plans.filter((p) => p.hardLimit).map((p) => p.id)).toEqual(["developer"]);
  });
});
