import { describe, expect, it } from "vitest"

import { APIProblemError, problemCode, problemMessage, problemMessages } from "./problems"

describe("API problem messages", () => {
  it("keeps the Chinese and English code sets identical", () => {
    expect(Object.keys(problemMessages.en).sort()).toEqual(Object.keys(problemMessages.zh).sort())
  })

  it("maps known server codes without exposing the server detail", () => {
    const problem = {
      code: "invalid_credentials",
      detail: "sensitive backend detail",
    }
    expect(problemCode(problem)).toBe("invalid_credentials")
    expect(problemMessage("zh", problemCode(problem))).not.toContain(problem.detail)
  })

  it("renders an unknown stable code verbatim", () => {
    expect(problemMessage("en", "future_server_code")).toBe("future_server_code")
  })

  it("normalizes transport failures to a safe client code", () => {
    expect(problemCode(new TypeError("fetch failed"))).toBe("request_failed")
    expect(new APIProblemError(undefined).code).toBe("request_failed")
  })
})
