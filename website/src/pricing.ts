import data from "./pricing.json";

/** A HookShield Cloud plan as shown on the pricing section (src/pricing.json). */
export type Plan = {
  id: "developer" | "pro" | "business" | "enterprise";
  monthlyCents: number | null; // null = custom contract
  yearlyCents: number | null; // a year paid at once; 0 = free, null = custom contract
  overageCentsPer100k: number | null; // per started block of 100,000 requests above the quota
  requests: number | null; // null = unlimited
  hardLimit: boolean;
  maxRetries: number | null; // null = custom rules / unlimited
  retentionHours: number;
  popular?: boolean;
};

export const plans = data.plans as Plan[];
