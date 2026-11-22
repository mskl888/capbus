# capbus

**装一次，你机器上所有 Agent 都会用。**

同一台电脑上装了 Claude Code、Codex、DSH、Cursor，每个都要单独配一遍 MCP server、单独填一遍 API key、单独装一份相同的工具。capbus 把这件事变成一次。

```bash
capbus supervise            # 一个后端，机器上只跑一份
capbus sync --shared        # 所有 Agent 指向它
```

---

## 它解决什么

一台机器上 N 个 Agent，每个都要求你：

- 在它自己的配置文件里加一遍 MCP server（`~/.claude.json`、`~/.codex/config.toml`、DSH profile、Cursor settings…）
- 再填一遍同样的 API key（于是同一把 key 散落在 4 个明文文件里）
- 再装一遍同样的工具（于是 4 个 Agent 各自冷启动一棵 `npx` 树，各占几百 MB）

capbus 做三件事，只做这三件：

1. **一份清单**——能力（MCP server / skill / CLI）只声明一次，存在一个 git 仓库里。
2. **一个进程**——无状态能力由 capbus 托管，多个 Agent 连同一个实例，不重复启动。
3. **N 份配置**——按每个 Agent 的格式自动生成并保持同步，你不手写任何配置。

---

## 它是怎么做到“一个进程”的

```
Agent A ──stdio──► capbus bridge github ──┐
                                          ├──► capbus supervise ──stdio──► npx .../server-github
Agent B ──stdio──► capbus bridge github ──┘         （唯一的一个后端进程）
```

- Agent 以为自己启动了一个普通的 stdio MCP server（`capbus bridge <name>`），实际上那就是个转发壳。
- 真正的后端只启动一次，由 supervisor 持有。
- 两个 Agent 都用 `id: 1` 发请求也不会串——supervisor 把 id 重写成全局唯一，收到应答再还原成各自的原始 id。
- `initialize` 由 supervisor 本地应答（后端只握手一次），所以第二个 Agent 不会重置第一个的状态。
- 能力级通知（如 `notifications/progress`）广播给所有已连接的 Agent。
- 后端崩了自动重启；Agent 手上正在等的请求会收到明确错误，而不是永远挂着。

后端与服务之间的连接只绑 `127.0.0.1`，并且要握手一个每次启动随机生成的 token——同机其他进程无法冒充 Agent。

---

## 不做什么

边界比功能重要。capbus **明确不做**：

- ❌ **不做 MCP 商店 / 目录站。** 已有 12 万个 server 被 Glama、Smithery、PulseMCP 索引完毕（见 [MARKET.md](MARKET.md)）。
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
| **Scope** | `global`（无状态，全机共享一个进程）/ `workspace`（有状态，每工作区一份）/ `session` |
| **Materializer** | 把能力"翻译"成某个 Agent 认得的配置格式的适配器 |
| **Supervisor** | 托管共享进程：启动、握手、健康、重启、多路复用 |

`scope` 是最重要的字段。有状态的能力**绝不能**全局共享——浏览器自动化 MCP 持有浏览器实例，两个 Agent 共用一个浏览器 = 互相跳到对方的页面。capbus 在 manifest 校验阶段就强制这条规则，写错直接拒绝：

```
! playwright.yaml: "playwright" is stateful and scope:global;
  stateful capabilities must be workspace or session scoped
  (two agents would share one live session)
```

---

## 命令

| 命令 | 作用 |
|---|---|
| `capbus doctor` | 检查能力目录、已装 Agent、supervisor 状态 |
| `capbus list` | 列出能力、作用域、是否持有会话 |
| `capbus supervise` | 前台运行共享后端；Ctrl-C 停止 |
| `capbus down` | 停止 supervisor 及其子进程 |
| `capbus sync [--shared] [--dry-run]` | 写进各 Agent 配置；`--shared` 指向共享后端 |
| `capbus bridge <name>` | stdio 转发壳（由 Agent 调用，你不要手动跑） |

支持的 Agent（`mcpServers` 方言）：**Claude Code、Cursor**。

尚未实现：Codex（TOML）与 DSH 适配器、`capbus install`（远端安装）、`capbus diff`（配置漂移）、Secret Broker（凭据集中）。

---

## 安装

```bash
git clone https://github.com/mskl888/capbus.git
cd capbus
go build -o capbus ./cmd/capbus
```

需要 Go 1.22+。

---

## 示例 manifest

```yaml
name: github
kind: mcp
scope: global          # 无状态 → 全机共享一个进程
source:
  npm: "@modelcontextprotocol/server-github"
  env:
    GITHUB_TOKEN: "ghp_..."   # 目前写在清单里；Secret Broker 之后会接管
permissions:
  egress: [api.github.com]
```

`examples/capabilities/` 里还有 `filesystem`、`playwright`（演示有状态能力必须 `workspace`），以及 `examples/fake-mcp/`——一个 50 行的假 MCP server，用来在没有 npm/docker 的情况下跑通 supervisor。

---

## 为什么不用现有的网关

因为现有的网关解决的是**远端路由**问题，capbus 解决的是**本地配置爆炸 + 进程重复**问题。

一个具体区别：你装了 4 个 Agent。现有网关让你在 4 个配置里各写一遍同一个 URL；capbus 让你什么都不写，而且那 4 个 Agent 连的是**同一个进程**。

---

## 许可证

**Apache-2.0**。见 [LICENSE](LICENSE)。

> 说明：capbus 是跑在用户本机的 CLI/守护进程，AGPL 在这里没有实际保护作用，只会吓退采用者。以后若出网络服务形态（hosted hub），那个组件再单独用 AGPL-3.0。
