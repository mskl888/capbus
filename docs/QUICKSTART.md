# 5 分钟上手

> 这份文档只有一个目标：让第一次用的人不卡住。
> 每一步都写了"你应该看到什么"，对不上就说明卡在哪一步。

---

## 你需要先有的东西

**一个 Agent。** Claude Code、Cursor、Codex、DSH 任意一个，装好并能正常对话即可。

**别的都不需要。** 不用注册账号，不用联网（除了装能力时 npm 要下载），不用会写配置。

---

## 三步

### 第一步：装 capbus

> ⚠️ **当前还没有发布 release**，所以下面的一行安装暂时不可用。
> 在那之前，用「从源码构建」的方式（需要先装 Go，这确实违背本文档的初衷——
> 一键安装脚本是下一步要做的事）。

**从源码构建**（需要 Go 1.22+）：

```bash
git clone https://github.com/mskl888/capbus.git
cd capbus
go build -o capbus ./cmd/capbus
```

Windows：

```powershell
git clone https://github.com/mskl888/capbus.git
cd capbus
go build -o capbus.exe ./cmd/capbus
```

**发布之后**，这两行会取代上面的步骤：

```bash
# Linux / macOS
curl -fsSL https://github.com/mskl888/capbus/releases/latest/download/install.sh | sh

# Windows (PowerShell)
irm https://github.com/mskl888/capbus/releases/latest/download/install.ps1 | iex
```

装完在终端里敲：

```bash
capbus version
```

应该看到类似这样一行：

```
capbus 0.4.0-dev (windows/amd64)
```

> 看到这个，你的机器就已经准备好了。下面的步骤随时可以中断，不会弄坏任何东西。

### 第二步：让它检查你的机器

```bash
capbus init
```

它会告诉你**只差哪几步**。例如：

```
capabilities   0 declared in C:\Users\me\.capbus\capabilities
agents         claude-code, codex
secrets        none missing
shared backend not running

next:
  1. capbus supervise --experimental   # one backend for tools-only capabilities; leave it running
  2. capbus sync            # write capabilities into every agent
  3. declare a capability in C:\Users\me\.capbus\capabilities
```

**照着 `next:` 里的编号做就行**，做完再跑一次 `capbus init`，直到它不再提示。

### 第三步：加第一个能力

新建一个文件 `~/.capbus/capabilities/github.yaml`（Windows 是 `C:\Users\你\.capbus\capabilities\github.yaml`），内容：

```yaml
name: github
kind: mcp
scope: global
source:
  npm: "@modelcontextprotocol/server-github"
permissions:
  secrets: [GITHUB_TOKEN]
```

然后：

```bash
capbus sync
```

应该看到：

```
wrote claude-code -> C:\Users\me\.claude.json
wrote codex -> C:\Users\me\.codex\config.toml
```

**重开一次 Agent**，它就能看到这个能力了。

---

## 关于密钥（重要，但很简单）

上面那份 `github.yaml` 声明了它需要一个 `GITHUB_TOKEN`。

**千万不要把 token 写进 yaml 文件。** 而是：

```bash
capbus secret set GITHUB_TOKEN
```

按下回车后粘贴 token，再按一次回车（Windows 按 `Ctrl+Z` 再回车，Linux/macOS 按 `Ctrl+D`）。

检查一下（只会显示名字，不会显示值）：

```bash
capbus secret list
```

这就是全部。token 存在加密文件里，**任何一个 Agent 的配置文件里都不会出现它**。

---

## 想让它更快？（可选，但推荐）

上面每次 Agent 启动，都会各自把能力进程再启一遍。**想让所有 Agent 共用一个后端**：

**第一个终端窗口**，跑着别关：

```bash
capbus supervise --experimental
```

**第二个终端窗口**：

```bash
capbus sync --shared
```

再重开 Agent。区别是：以前 4 个 Agent 各起一份（各冷启动一次，各占几百 MB），现在只有一份。

想停止共享，在第一个窗口按 `Ctrl+C`，然后 `capbus sync` 回到各自启动。

---

## 出问题了怎么办

| 现象 | 怎么办 |
|---|---|
| `capbus: command not found` | 关掉终端重开一次（PATH 需要刷新） |
| Agent 里看不到能力 | `capbus doctor` 看它有没有检测到你的 Agent |
| 提示缺 secret | `capbus secret set 那个名字` |
| `--shared` 报错 | 先开一个窗口跑 `capbus supervise --experimental`（共享模式是实验性的） |
| 不确定现在是什么状态 | `capbus init`，它永远告诉你下一步 |

**出问题时先跑这两个，90% 的情况它们会直接告诉你原因：**

```bash
capbus init
capbus doctor
```

---

## 卸载

```bash
capbus down                    # 停掉共享后端（如果开了）
rm -rf ~/.capbus               # 删掉所有配置和密钥
```

然后把 Agent 配置里 `capbus` 相关的条目删掉（`~/.claude.json`、`~/.codex/config.toml` 等）。删之前想留个备份就先复制一份。

---

## 不想敲命令？

**把下面这段完整复制给你的 Agent，让它替你做：**

```
请帮我用 capbus 管理这台机器上的能力。步骤：
1. 先运行 capbus init 看当前状态，把输出贴给我。
2. 按它提示的 next 步骤执行，每一步做完告诉我结果。
3. 需要密钥时不要问我值，只告诉我要运行哪个 capbus secret set 命令。
4. 不要手动编辑任何 Agent 的配置文件，一律用 capbus sync。
```

更详细的"给 AI 用"的说明见 [AI.md](AI.md)。
