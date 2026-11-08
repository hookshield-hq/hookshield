import data from "./pricing.json";

/** A HookShield Cloud plan as shown on the pricing section (src/pricing.json). */
export type Plan = {
  id: "developer" | "pro" | "business" | "enterprise";
  monthlyCents: number | null; // null = custom contract
  yearlyCents: number | null; // a year paid at once; 0 = free, null = custom contract
  overageCentsPer10k: number | null;
  requests: number | null; // null = unlimited
  hardLimit: boolean;
  maxRetries: number | null; // null = custom rules / unlimited
  retentionHours: number;
  popular?: boolean;
};

export const plans = data.plans as Plan[];
