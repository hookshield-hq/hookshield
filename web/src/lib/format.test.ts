import { describe, expect, it } from "vitest";
import { formatMs, formatNumber, formatPercent, formatRelative } from "./format";

describe("format (per locale)", () => {
  it("formats numbers and percentages with the locale's separators", () => {
    expect(formatNumber("en", 1234.5)).toBe("1,234.5");
    expect(formatNumber("pt-BR", 1234.5)).toBe("1.234,5");
    expect(formatPercent("en", 99.98, 2)).toBe("99.98%");
    expect(formatPercent("pt-BR", 75)).toBe("75%");
  });

  it("formats relative time in each language", () => {
    const now = new Date("2026-11-03T12:00:00Z");
    const twoMinAgo = new Date("2026-11-03T11:58:00Z");
    expect(formatRelative("en", twoMinAgo, now)).toBe("2 minutes ago");
    expect(formatRelative("pt-BR", twoMinAgo, now)).toBe("há 2 minutos");
    expect(formatRelative("es", twoMinAgo, now)).toBe("hace 2 minutos");
  });

  it("keeps latency precision readable", () => {
    expect(formatMs("en", 0.4213)).toBe("0.42 ms");
    expect(formatMs("en", 12.34)).toBe("12.3 ms");
    expect(formatMs("en", 1204.4)).toBe("1,204 ms");
  });
});
