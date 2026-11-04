import { describe, expect, it } from "vitest";
import { formatDuration, retrySchedule, splitDuration } from "./duration";

const base = { max_retries: 5, initial_interval_ms: 10_000, multiplier: 2, max_delay_ms: 3_600_000, custom_schedule_ms: [] };

describe("retrySchedule", () => {
  it("matches the documented default schedule (10, 20, 40, 80, 160 s)", () => {
    expect(retrySchedule({ ...base, strategy: "exponential" })).toEqual([10_000, 20_000, 40_000, 80_000, 160_000]);
  });
  it("caps every delay at max_delay_ms", () => {
    expect(retrySchedule({ ...base, strategy: "exponential", max_delay_ms: 30_000 })).toEqual([10_000, 20_000, 30_000, 30_000, 30_000]);
  });
  it("repeats the initial interval for fixed", () => {
    expect(retrySchedule({ ...base, strategy: "fixed", max_retries: 3 })).toEqual([10_000, 10_000, 10_000]);
  });
  it("repeats the last custom delay", () => {
    expect(retrySchedule({ ...base, strategy: "custom", custom_schedule_ms: [5_000, 60_000], max_retries: 4 })).toEqual([5_000, 60_000, 60_000, 60_000]);
  });
  it("is empty without retries and never below 1 s", () => {
    expect(retrySchedule({ ...base, strategy: "fixed", max_retries: 0 })).toEqual([]);
    expect(retrySchedule({ ...base, strategy: "custom", custom_schedule_ms: [10], max_retries: 1 })).toEqual([1000]);
  });
});

describe("durations", () => {
  it("splits into the largest exact unit", () => {
    expect(splitDuration(10_000)).toEqual({ value: 10, unit: "s" });
    expect(splitDuration(120_000)).toEqual({ value: 2, unit: "min" });
    expect(splitDuration(3_600_000)).toEqual({ value: 1, unit: "h" });
    expect(splitDuration(90_000)).toEqual({ value: 90, unit: "s" });
  });
  it("formats compactly", () => {
    expect(formatDuration("en", 10_000)).toBe("10 s");
    expect(formatDuration("en", 90_000)).toBe("1 min 30 s");
    expect(formatDuration("en", 5_400_000)).toBe("1 h 30 min");
    expect(formatDuration("en", 172_800_000)).toBe("2 d");
  });
});
