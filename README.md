# capbus

**装一次，你机器上所有 Agent 都会用。**

同一台电脑上装了 Claude Code、Codex、DSH、Cursor，每个都要单独配一遍 MCP server、单独填一遍 API key、单独装一份相同的工具。capbus 把这件事变成一次。

```bash
capbus install github      # 装一次
capbus sync                # 所有 Agent 立刻都能用
```

---

## 它解决什么

一台机器上 N 个 Agent，每个都要求你：

- 在它自己的配置文件里加一遍 MCP server（`~/.claude.json`、`~/.codex/config.toml`、DSH profile、Cursor settings…）
- 再填一遍同样的 API key（于是同一把 key 散落在 4 个明文文件里）
- 再装一遍同样的工具（于是 `npx playwright` 缓存了 4 份）

capbus 做三件事，只做这三件：

1. **一份清单**——能力（MCP server / skill / CLI）只声明一次，存在一个 git 仓库里。
2. **一个进程**——无状态能力由 capbus 统一托管，多个 Agent 连同一个端点，不重复启动。
3. **N 份配置**——按每个 Agent 的格式自动生成并保持同步，你不手写任何配置。

---

## 不做什么

边界比功能重要。capbus **明确不做**：

- ❌ **不做 MCP 商店 / 目录站。** 已有 12 万个 server 被 Glama、Smithery、PulseMCP 索引完毕，再做一个目录没有价值（见 [MARKET.md](MARKET.md)）。
- ❌ **不做通用网关 / 路由 / 多 provider 负载均衡。** 已有 6,884 个项目在做，包括 Kong 和 OmniRoute。
- ❌ **不做 Agent 框架。** 不实现 loop、不管理对话、不碰提示词。capbus 只管"能力"，不管"人格"。
- ❌ **不代理模型流量。** 模型接入是你的 Agent 自己的事。

一句话：**别人做"让 Agent 连上工具"，capbus 做"让你不用为每个 Agent 再配一遍"。**

---

## 核心概念

| 概念 | 含义 |
|---|---|
| **Capability** | 一项能力：一个 MCP server、一个 skill 目录、一个 CLI 工具 |
| **Manifest** | 描述能力的 YAML：来源、版本、权限、作用域 |
| **Scope** | `global`（无状态，全机共享）/ `workspace`（有状态，每工作区一份）/ `session` |
| **Materializer** | 把能力"翻译"成某个 Agent 认得的配置格式的适配器 |
| **Supervisor** | 托管共享进程：启动、健康检查、重启、日志 |

`scope` 是最重要的字段。有状态的能力**绝不能**全局共享——浏览器自动化 MCP 持有浏览器实例，两个 Agent 共用一个浏览器 = 互相跳到对方的页面。capbus 在 manifest 校验阶段就强制这条规则。

---

## 状态

**v0.1.0-dev，可编译、可运行、有测试。**

已有：

| 命令 | 作用 |
|---|---|
| `capbus doctor` | 检查能力目录、已装 Agent、配置路径；不通过返回非 0 |
| `capbus list` | 列出能力、作用域、是否持有会话 |
| `capbus sync [--dry-run]` | 写进每个已检测到的 Agent 配置；保留该 Agent 自己的字段，无法解析的文件拒绝覆盖 |
| `capbus version` | |

目前支持的 Agent（`mcpServers` 方言）：**Claude Code、Cursor**。

尚未实现（按顺序）：

- `capbus supervise` — 托管共享 MCP 进程（一个进程、多个 Agent 连接同一个端点）
- `capbus install <name>` — 从远端清单拉取并安装
- `capbus diff` — 检测配置漂移
- Codex（TOML）、DSH、VS Code 适配器
- 密钥集中（Secret Broker）

---

## 安装

```bash
# 从源码（需要 Go 1.22+）
git clone https://github.com/<you>/capbus.git
cd capbus
go build -o capbus ./cmd/capbus
```

---

## 示例 manifest

```yaml
name: github
kind: mcp
source:
  oci: ghcr.io/modelcontextprotocol/server-github:0.6
scope: global          # 无状态 → 全机共享一个进程
placement: host
permissions:
  egress: [api.github.com]
  secrets: [GITHUB_TOKEN]
health:
  probe: tools/list
  interval: 30s
```

---

## 为什么不用现有的网关

因为现有的网关解决的是**远端路由**问题，capbus 解决的是**本地配置爆炸**问题。

一个具体区别：你装了 4 个 Agent。现有网关让你在 4 个配置里各写一遍同一个 URL；capbus 让你什么都不写。

---

## 许可证

**Apache-2.0**。见 [LICENSE](LICENSE)。

> 说明：capbus 是跑在用户本机的 CLI/守护进程，AGPL 在这里没有实际保护作用，只会吓退采用者。以后若出网络服务形态（hosted hub），那个组件再单独用 AGPL-3.0。
