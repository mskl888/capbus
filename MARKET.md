# 市场调研与立项依据

> 调研时间：2026-11-22
> 数据来源：GitHub Search API（`sort=stars`, 未认证）、[Glama](https://glama.ai/mcp/servers)、[ZimaOS App Store](https://www.zimaspace.com/docs/jp/zimaos/app-store/)
> 目的：判断"Agent 能力底座 / Agent OS"这一命题是否还有可进入的空间，以及可进入的具体位置。

---

## 0. 结论先行

1. **通用层已经饱和。** `mcp gateway` 相关仓库 6,884 个，`agent os` 相关仓库 16,365 个，公开索引的 MCP server 约 12 万个。头部项目星数 1.4 万–8.9 万，且多数在 2026 年当年创建。
2. **"MCP / Skill 商店"作为产品已经死亡。** 内容层被 Glama / Smithery / PulseMCP 等索引完毕，再做一个目录站没有价值。
3. **通用"能力底座"（网关 + 商店）不适合作为创业切入点。** 6,884 个免费项目构成的定价天花板是 0；付费意愿集中在企业治理，而企业治理已有多个成熟开源项目。
4. **仍有两个空白**：①本地多 Agent 的配置统一（"装一次，所有 Agent 都会用"）；②垂直行业工作台（把隔离 / 审批 / 接管打包给一个具体行业）。
5. **本项目只做 ①，并把 ② 作为商业方向。** 通用 OS 降级为内部架构，不作为对外品牌。

---

## 1. 检索方法与原始数据

| 查询词 | GitHub 仓库总数 | 说明 |
|---|---|---|
| `mcp gateway` | **6,884** | 工具路由 / 网关 / 代理 |
| `agent os` | **16,365** | 名称或描述含 "agent os" 的仓库 |
| MCP server 索引 | **120,509** | [Glama](https://glama.ai/mcp/servers)，2026-11 |

> 星数可以刷，不能单独作为判断依据。但**仓库总数是结构性的，无法伪造**：6,884 / 16,365 这两个数字本身已经说明问题。

---

## 2. MCP 网关 / AI 网关（`mcp gateway`，6,884 个）

| 项目 | ★ | 许可证 | 创建 | 定位 |
|---|---:|---|---|---|
| [cheaper-inference/OmniRoute](https://github.com/cheaper-inference/OmniRoute) | 89.4k | MIT | 2026-02 | AI 网关，375 家 provider、1400+ 模型、MCP/A2A |
| [Kong/kong](https://github.com/Kong/kong) | 44.4k | Apache-2.0 | 2014-11 | 老牌 API 网关，已加 MCP 支持 |
| [casdoor/casdoor](https://github.com/casdoor/casdoor) | 14.9k | Apache-2.0 | 2020-10 | "Agent-first IAM / MCP & agent gateway" |
| [Portkey-AI/gateway](https://github.com/Portkey-AI/gateway) | 13.4k | MIT | 2023-08 | AI 网关 1600+ LLM + guardrails |
| [octelium/octelium](https://github.com/octelium/octelium) | 4.2k | AGPL-3.0 | 2025-05 | 零信任接入 + MCP 网关 + 隧道（**接入与网关合体**） |
| [ascending-llc/jarvis-registry](https://github.com/ascending-llc/jarvis-registry) | 4.1k | Apache-2.0 | 2025-10 | 企业 MCP/Agent 网关：身份、访问控制、可观测性（**本项目的企业版形态已被占据**） |
| [yvgude/lean-ctx](https://github.com/yvgude/lean-ctx) | 4.0k | Apache-2.0 | 2026-03 | Context gateway，Rust |
| [butterbase-ai/butterbase](https://github.com/butterbase-ai/butterbase) | 3.0k | Apache-2.0 | 2026-05 | BaaS + AI gateway + MCP |
| [supercorp-ai/supergateway](https://github.com/supercorp-ai/supergateway) | 3.0k | MIT | 2024-12 | stdio ↔ HTTP/SSE 转换 |

**判读**：这一层的竞争维度是"路由 + 鉴权 + 可观测性"，玩家从大厂（Kong/Tyk）到个人项目全谱系覆盖。**没有差异化空间，也没有定价空间。**

---

## 3. Agent OS（`agent os`，16,365 个）

| 项目 | ★ | 许可证 | 创建 | 定位 |
|---|---:|---|---|---|
| [zeroclaw-labs/zeroclaw](https://github.com/zeroclaw-labs/zeroclaw) | 33.6k | Apache-2.0 | 2026-02 | "any OS, any platform, deploy anywhere" 的自主助手基础设施，Rust |
| [trycua/cua](https://github.com/trycua/cua) | 32.8k | MIT | 2025-01 | computer-use 沙箱，跨 OS，训练/评测/数据生成 |
| [kortix-ai/CompanyOS](https://github.com/kortix-ai/CompanyOS) | 20.8k | NOASSERTION | 2024-10 | "公司的操作系统"，多人协作 Agent，可自托管 |
| [browseros-ai/BrowserOS](https://github.com/browseros-ai/BrowserOS) | 14.4k | AGPL-3.0 | 2025-05 | Agentic 浏览器 |
| [holaboss-ai/holaOS](https://github.com/holaboss-ai/holaOS) | 11.4k | NOASSERTION | 2026-03 | agent-os / harness / workspace / memory |
| [osaurus-ai/osaurus](https://github.com/osaurus-ai/osaurus) | 8.2k | MIT | 2025-08 | macOS agent harness，Swift |
| [craft-ai-agents/craft-agents-oss](https://github.com/craft-ai-agents/craft-agents-oss) | 7.3k | Apache-2.0 | 2026-01 | Agent 工作区 |
| [Q00/ouroboros](https://github.com/Q00/ouroboros) | 6.4k | MIT | 2026-01 | "Agent OS"，MCP server，14 种运行时接入 |
| [OpenBMB/AgentVerse](https://github.com/OpenBMB/AgentVerse) | 5.0k | — | 2023 | 多智能体研究框架（学术向，已低频） |
| [AIOS](https://ar5iv.labs.arxiv.org/html/2403.16971) | — | 论文 | 2024-03 | "LLM Agent Operating System"，kernel 调度隐喻 |

**判读**：
- "Agent OS" 这个词已经被学术界（AIOS 论文）和社区（30+ 个同名项目）消耗完毕，**不再具备品牌区分度**。
- 头部项目的共同点是**单人/单团队的 Agent 宿主**，不是"一台机器上多 Agent 共存 + 治理"。
- 星数增长极快（2026 年当年创建的占多数），说明**这是抢滩期，不是稳态期**；对个人开发者意味着窗口在关闭，而不是在打开。

---

## 4. 相邻赛道：NAS / 家庭服务器（直接威胁）

| 项目 | 状态 | 与本项目的关系 |
|---|---|---|
| [ZimaOS App Store](https://www.zimaspace.com/docs/jp/zimaos/app-store/) | ZimaOS 已把 **DeepSeek Harness**、**OpenClaw Agent** 直接作为应用上架 | **"飞牛式 OS + Agent" 的位置正在被占** |
| 飞牛 fnOS | 闭源，Web 桌面 + 应用中心 + 远程访问，国内用户心智强 | 体验对标物；闭源是它的软肋 |
| CasaOS / Umbrel / Runtipi | 自托管应用商店 | 有商店生态，无 Agent 治理 |
| Proxmox / Incus | 虚拟化与系统容器 | 本项目应复用，不应重写 |

**判读**：NAS 厂商正在把 Agent 当"应用"装进系统，但**都没有做会话隔离、输入租约、人工审批、审计**。这是本项目唯一还站得住的位置——不在"能不能装"，在"装了之后谁来管"。

---

## 5. 空白点（机会）

| 空白 | 现有项目为何没做 | 价值 | 可收费性 |
|---|---|---|---|
| **本地多 Agent 配置统一**（装一次，Claude Code / Codex / DSH / Cursor 全都会用；共享进程、共享凭据） | 6,884 个网关都在做"远端路由"，没人做"本地配置转译" | 真实、日常、可验证 | ❌ 低（开发者习惯免费） |
| **单机 Agent 治理**（租约、手机审批、审计） | 企业网关面向组织，个人场景无人做 | 真实 | ⚠️ 中（个人不付费，小团队可能） |
| **垂直行业工作台**（多账号内容工厂、跨境电商等） | 需要行业 know-how，通用项目做不了 | 高 | ✅ 高 |

---

## 6. 战略决定

1. **不做通用 Agent OS，不做 MCP 商店，不做通用网关。**
2. **开源项目 = 本地多 Agent 物化器（本项目 capbus）。** 范围极小，只解决"装一次，所有 Agent 都会用"。
3. **商业化 = 垂直工作台。** 用 capbus 做底座，卖给具体行业的运营团队，卖交付与服务，不卖软件许可。
4. **"Agent OS" 作为内部架构词使用，对外不以它命名。**

理由：通用层的赢家需要团队、融资、和持续两周一次的发版节奏；个人开发者的比较优势在**对具体工作流的理解**，不在基础设施的重复实现。

---

## 7. 止损线

- 6 个月内，若能说出 **3 个愿意付 ¥5,000/年** 的具体客户名字 → 继续。
- 若不能 → capbus 降级为个人项目与简历资产，停止投入。

---

## 8. 附：本次检索的原始查询

```
GET https://api.github.com/search/repositories?q=mcp+gateway&sort=stars&order=desc
  → total_count = 6884

GET https://api.github.com/search/repositories?q=agent+os&sort=stars&order=desc
  → total_count = 16365
```

> 复现方式：把上面的 URL 直接在浏览器打开即可，无需 token（有速率限制）。
