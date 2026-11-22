# 给 AI 用：三种接入方式

> 面向两批人：想让 AI 帮自己管能力的用户，和正在写 Agent 集成的开发者。
> 关键认知：**AI 不读你的 README，它只读工具描述。** 所以下面每种方式都自带"模型能看懂"的说明。

---

## 方式一：把 capbus 自己变成 MCP server（推荐）

capbus 有一个 `mcp` 子命令，它本身就是一个 MCP server。接上之后，AI 可以用**工具调用**管理能力，而不是让你敲命令。

**Claude Code / Cursor**（`~/.claude.json` 或 `~/.cursor/mcp.json`）：

```json
{
  "mcpServers": {
    "capbus": { "command": "capbus", "args": ["mcp"] }
  }
}
```

**Codex**（`~/.codex/config.toml`）：

```toml
[mcp_servers.capbus]
command = "capbus"
args = ["mcp"]
```

**Windows 注意**：`command` 要写绝对路径，例如 `C:\\Users\\你\\AppData\\Local\\capbus\\capbus.exe`。

接好之后，AI 会看到四个工具：

| 工具 | 作用 |
|---|---|
| `capbus_list` | 现在有哪些能力、共享后端是否在跑。**AI 应该先调这个。** |
| `capbus_register` | 传一份 YAML，注册一个新能力 |
| `capbus_sync` | 把能力写进每个已装 Agent 的配置 |
| `capbus_status` | 诊断：谁装了、谁缺密钥 |

**为什么这是正解**：AI 只需要会调四个受控的工具，不需要 `shell` 权限。你给它的是"能力管理"，不是"任意命令执行"。这个区别在安全上是数量级的。

### 工具的语义约束（写死在描述里）

- `capbus_register` 的描述明确写了：**不要往 manifest 里放凭据**，要列在 `permissions.secrets` 里，然后让用户自己跑 `capbus secret set`。
- 如果用户把 token 贴进对话，AI 得到的指令是"不要存，建议改成通过 stdin 传给 CLI"。
- `capbus_sync shared=true` 会检查共享后端是否真的在跑，不会假装成功。

---

## 方式二：把这段当作 system prompt / 技能

任何支持自定义指令的 Agent 都可以用（Claude 的 Skills、DSH 的 preset、Cursor 的 rules、自研 Agent 的 system prompt）。

```markdown
你可以使用 capbus 管理这台机器上的 MCP 能力。规则：

1. 先调用 capbus_list，看已有什么，不要重复注册。
2. 注册新能力用 capbus_register，传完整 YAML。必填 name / kind / scope / source。
   有活状态的能力（浏览器、桌面）必须 scope: workspace，否则会被拒绝——
   两个 Agent 共用一个浏览器会互相打断。
3. 注册完调用 capbus_sync 才会生效，用户需要重开 Agent。
4. 凭据永远不要写进 manifest。在 permissions.secrets 里写环境变量名，
   然后让用户自己运行 capbus secret set NAME。
   用户如果直接把 token 发给你，不要保存它，告诉他改用命令行从 stdin 输入。
5. 不要手动编辑 ~/.claude.json 或 ~/.codex/config.toml。
   一切经过 capbus_sync，否则下次同步会覆盖你的修改。
6. 用户说"看不到能力"时，调用 capbus_status 诊断，不要猜。
```

**第 5 条是最容易出事的地方**：AI 会本能地直接改配置文件，然后下一次 `sync` 把它改回来，用户就觉得"这东西不靠谱"。

---

## 方式三：只用命令行（AI 不需要接 MCP）

如果 Agent 已经能跑 shell，让它调用 CLI 也完全可以。区别只是少了工具级约束。

```bash
capbus init                    # 永远先看状态
capbus list                    # 有哪些能力
capbus sync                    # 写进配置
capbus doctor                  # 诊断
capbus secret list             # 缺哪些密钥（不显示值）
```

---

## 一条给 Agent 的硬规则

**不要把凭据放进对话上下文。**

原因不是洁癖：对话内容会进入模型上下文、日志、缓存、以及下一次会话的摘要。一旦 token 进去过，最彻底的补救只有去服务商那边吊销重发。

正确的形状永远是：

```
用户：我想要 GitHub 能力
AI  ：已注册 github 能力。它需要 GITHUB_TOKEN——
      请你自己运行：capbus secret set GITHUB_TOKEN
      然后把 token 粘到终端里（不要发给我）。
AI  ：（用户确认后）现在调用 capbus_sync。
```

capbus 的加密存储就是为了让这条规则**在结构上成立**：Agent 的配置文件里没有位置放 token，所以即使 AI 想"帮忙存一下"也存不进去。

---

## 给开发者的补充

capbus 的 manifest 是一个可以程序化生成的契约：

```yaml
name: fs                       # 唯一标识，小写无空格
kind: mcp | skill | cli
scope: global | workspace | session    # 有状态的一律 workspace
stateful: true                 # 声明后 global 会被直接拒绝
source:
  npm: "@modelcontextprotocol/server-filesystem"   # 或 oci / path / command
  args: ["/tmp"]               # 可选，追加在默认参数后
  env: { PATH: "/opt/node/bin:/usr/bin" }          # 可选，覆盖继承值
permissions:
  egress: [api.github.com]     # 声明式，尚未强制执行
  secrets: [GITHUB_TOKEN]      # 启动时注入环境，不落盘
```

`egress` 目前只是声明，**没有强制**——这一点不要对用户夸大。
