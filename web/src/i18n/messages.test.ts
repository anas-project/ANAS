import { describe, expect, it } from "vitest"

import { initialLocale, messages } from "./messages"

describe("console messages", () => {
  it("keeps the zh and en key sets identical", () => {
    expect(Object.keys(messages.en).sort()).toEqual(Object.keys(messages.zh).sort())
  })

  it("selects Chinese when any preferred language is Chinese", () => {
    expect(initialLocale(["en-SG", "zh-CN"])).toBe("zh")
    expect(initialLocale(["en-SG"])).toBe("en")
  })

  it("keeps every required LAN warning fact in both languages", () => {
    for (const locale of ["zh", "en"] as const) {
      const warning = messages[locale].lanRisk
      expect(warning).toContain("ssh -L")
      expect(warning).toContain("anas console tls --self-signed")
    }
    expect(messages.zh.lanRisk).toContain("主动攻击者")
    expect(messages.zh.lanRisk).toContain("防火墙")
    expect(messages.en.lanRisk).toContain("active attacker")
    expect(messages.en.lanRisk).toContain("firewall")
  })
})
