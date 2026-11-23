# capbus

**声明一次，每个 Agent 按自己的格式装上。**

同一台电脑上装了 Claude Code、Codex、DSH、Cursor，每个都要单独配一遍 MCP server、单独填一遍 API key、单独装一份相同的工具。capbus 把「声明」这件事变成一次。

```bash
capbus sync                       # 把清单翻译成每个 Agent 自己的配置
capbus supervise --experimental   # 可选：工具类能力共用一个后端（实验性）
```

**第一次用 → [5 分钟上手](docs/QUICKSTART.md)** ｜ **想让 AI 自己管 → [给 AI 用](docs/AI.md)**

---

## 它解决什么

一台机器上 N 个 Agent，每个都要求你：

- 在它自己的配置文件里加一遍 MCP server（`~/.claude.json`、`~/.codex/config.toml`、DSH profile、Cursor settings…）
- 再填一遍同样的 API key（于是同一把 key 散落在 4 个明文文件里）
- 再装一遍同样的工具（于是 4 个 Agent 各自冷启动一棵 `npx` 树，各占几百 MB）

capbus 做三件事，只做这三件：

1. **一份清单**——能力（MCP server / skill / CLI）只声明一次，存在一个 git 仓库里。
2. **一个进程（实验性）**——工具类能力可以由 capbus 托管，多个 Agent 连同一个实例。代价是：后端只握手一次、收不到服务端反过来问客户端的话、通知发给所有接入者。**只适合 tools-only 能力**；需要自己会话的能力请保持不共享。
3. **N 份配置**——按每个 Agent 的格式自动生成并保持同步，你不手写任何配置。只动 capbus 自己写过的条目，用户手动加的一个字节都不碰。

**边界，说清楚**（2026-11-23 架构评审后收窄）：

| 不要这样理解 | 实际是这样 |
|---|---|
| 共享后端降低每轮 token | 每个 Agent 仍收到完整工具描述；降 token 靠**按需启用 + 结果裁剪** |
| 所有 Agent 共享插件 | 宿主 API 与加载格式不同；capbus 只共享**声明**，各 Agent 原生安装 |
| 单二进制零依赖 | **capbus 本体**单二进制；能力本身仍可能需要 npx / docker / 运行时 |
| 同机进程无法冒充 Agent | 防跨用户和误连接；**不防**同一个用户下的恶意进程 |

---

## 坦诚对比：你该选哪个

capbus 不是所有人的答案。先说清楚什么时候**不要**用它：

| 你的情况 | 更好的选择 |
|---|---|
| 只有一个 Agent，想装几个插件 | 直接用那个 Agent 自己的插件市场，别加中间层 |
| 需要云端托管、团队共享、SSO | 用企业网关（Kong、Casdoor、jarvis-registry） |
| 只要浏览器自动化 | 直接装 Playwright MCP，capbus 帮不上忙 |
| 想让 AI 记住事情 | 用记忆系统（如 [agent-memory](https://github.com/yb-zhai/agent-memory)），capbus 只负责把它接进来，并保证只有一个实例 |

**那什么时候用 capbus？** 同时满足这三条：

1. 一台机器上有**两个以上** Agent（Claude Code / Cursor / Codex / DSH …）
2. 你发现同一个插件装了三四遍，或者根本不知道**它到底有没有被调用过**
3. 你不想为"没在用"的插件每轮付 token

三条只中一条，capbus 带来的复杂度就不划算。

## 不做什么

边界比功能重要。capbus **明确不做**：

- ❌ **不做 MCP 商店 / 目录站。** 已有十几万个 server 被 Glama、Smithery、PulseMCP 索引完毕（见 [MARKET.md](MARKET.md)）。
- ❌ **不做通用网关 / 路由 / 多 provider 负载均衡。** 已有数千个项目在做，包括 Kong 和 OmniRoute。
- ❌ **不做 Agent 框架。** 不实现 loop、不管理对话、不碰提示词。
- ❌ **不代理模型流量。** 模型接入是你的 Agent 自己的事。
- ❌ **不做第二个记忆系统。** 记忆是能力，不是插座的功能。

一句话：**别人做"让 Agent 连上工具"，capbus 做"让你不用为每个 Agent 再配一遍，并且知道它到底有没有在工作"。**

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

## 凭据：存一次，任何 Agent 配置里都不出现

一个 token 被复制进四个 Agent 的配置，是这类工具最真实的危险——它会出现在你的截图里、备份里、`git add` 里，以及某个 Agent 读进去的上下文里。

```bash
echo "$GITHUB_TOKEN" | capbus secret set GITHUB_TOKEN    # 加密存一次
capbus secret list                                       # 只显示名字和时间
```

之后，能力在启动的那一刻由 capbus 注入环境变量，**Agent 的配置文件里只有 `capbus exec <name>`，没有任何密钥**：

```json
{
  "mcpServers": {
    "github": { "command": "/usr/local/bin/capbus", "args": ["exec", "github"] }
  }
}
```

丢失的密钥不会变成空字符串——它会明确失败并告诉你怎么补：

```
capbus exec: github: missing secret(s) GITHUB_TOKEN — set with `capbus secret set <NAME>`
```

**威胁模型，说清楚**：主密钥就在同一台机器上（`~/.capbus/master.key`，或 `CAPBUS_MASTER_KEY` 环境变量），所以以你的用户身份运行的进程能读到它。它防的是**凭据扩散**——四处复制、误提交、截图、被 Agent 读进上下文。它**不防**以你身份运行的恶意软件，也不是 Vault。要更强，就把主密钥放进系统钥匙串或你的 shell profile，别和密文放一起。




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
| `capbus supervise --experimental` | 前台运行共享后端（实验性，不带该参数会拒绝并列出代价）；Ctrl-C 停止 |
| `capbus down` | 停止 supervisor 及其子进程 |
| `capbus sync [--shared] [--dry-run]` | 写进各 Agent 配置；`--shared` 指向共享后端 |
| `capbus bridge <name>` | stdio 转发壳（由 Agent 调用，你不要手动跑） |
| `capbus exec <name>` | 以子进程方式运行一个能力，并注入它的密钥 |
| `capbus secret list/set/get/rm` | 管理加密的凭据库 |
| `capbus mcp` | 把 capbus 自己作为 MCP server 跑起来，Agent 用工具调用管理能力 |
| `capbus init` | 检查状态，只输出你真正缺的那几步 |

支持的 Agent：

| Agent | 方言 | 写入位置 |
|---|---|---|
| Claude Code | JSON `mcpServers` | `~/.claude.json` |
| Cursor | JSON `mcpServers` | `~/.cursor/mcp.json` |
| Codex | TOML `[mcp_servers.<name>]` | `~/.codex/config.toml` |
| DSH | bundle（`package.json` + `cordis.patch.yml`） | `~/.capbus/dsh-bundle/` |

同一份清单，四种产出：

```toml
# ~/.codex/config.toml
[mcp_servers.github]
args = ['exec', 'github']
command = '/usr/local/bin/capbus'
```

```yaml
# ~/.capbus/dsh-bundle/cordis.patch.yml
- insert:
    - id: 'capbus-github'
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: 'github'
        transport: stdio
        command: '/usr/local/bin/capbus'
        args: ['exec', 'github']
        failOnStartupError: true
```

两个注意事项：

- **DSH 的 bundle 需要你自己安装一次**——它会改动 profile，capbus 不替你做。
- **Codex 的 TOML 是整文件重写**，注释会丢。无法解析的配置文件一律拒绝改写，不会静默覆盖。

尚未实现：`capbus install`（远端安装）、`capbus diff`（配置漂移）、`workspace` 作用域的进程托管。

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

## 跨 Agent 共享（记忆、缓存、队列）

有些能力**只有一份才有意义**：记忆库、缓存、任务队列。两个 Agent 各有一份，它们的状态就会悄悄分叉——而且不会报错。

给它加上 `shared: true`：

```yaml
name: memory
kind: mcp
scope: global
shared: true              # 只有一份，所有 Agent 连同一个进程
source:
  command: 'C:\path\to\memory-mcp.exe'
```

然后：

```bash
capbus supervise --experimental   # 一个后端，一份数据（实验性）
capbus sync               # 每个 Agent 都拿到 bridge，指向它
```

**忘了开后端会怎样？** `sync` 会直接拒绝，而不是静默写一份各自的副本：

```
capbus sync: these capabilities must be shared, but no shared backend is running:
  memory
Start `capbus supervise --experimental` first. Writing per-agent copies would let their state diverge silently.
```

这就是这个字段存在的全部理由：**让"记忆分叉"变成一个必须解决的错误，而不是一个几个月后才发现的现象。**

和"多个进程共用一个 JSONL 文件"相比，这里只有一个进程，不存在"把它加载进内存、看不到别人的更新、要重启才同步"的问题。
## 许可证

**Apache-2.0**。见 [LICENSE](LICENSE)。

> 说明：capbus 是跑在用户本机的 CLI/守护进程，AGPL 在这里没有实际保护作用，只会吓退采用者。以后若出网络服务形态（hosted hub），那个组件再单独用 AGPL-3.0。
