import { describe, expect, it } from "vitest";
import { ApiError, errorKey, toApiError } from "./errors";

describe("API errors", () => {
  it("reads the stable code and details of an error body", () => {
    const e = toApiError(422, { error: { code: "transform.invalid", details: [{ section: "body", index: 0 }] } });
    expect(e).toBeInstanceOf(ApiError);
    expect(e.status).toBe(422);
    expect(e.code).toBe("transform.invalid");
    expect(e.details).toEqual([{ section: "body", index: 0 }]);
  });

  it("falls back when the body has no code", () => {
    expect(toApiError(502, undefined).code).toBe("internal");
    expect(toApiError(418, "teapot").code).toBe("unknown");
  });

  it("maps unknown codes to a generic message key", () => {
    const has = (k: string) => k === "auth.invalid_credentials";
    expect(errorKey("auth.invalid_credentials", has)).toBe("auth.invalid_credentials");
    expect(errorKey("brand.new_code", has)).toBe("unknown");
  });
});
