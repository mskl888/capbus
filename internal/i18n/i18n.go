// Package i18n renders messages in the user's language.
//
// The English sentence is the key. That keeps call sites readable, needs no key
// registry, and means a message with no translation yet still prints something
// useful instead of a placeholder.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

// Lang returns the active language: "zh" or "en".
//
// Default is English because this is a published project. The double-click
// launchers set CAPBUS_LANG=zh, so the desktop entry speaks Chinese while the
// repository and CI stay English.
func Lang() string {
	l := strings.ToLower(strings.TrimSpace(os.Getenv("CAPBUS_LANG")))
	if strings.HasPrefix(l, "zh") {
		return "zh"
	}
	return "en"
}

// T formats a message in the active language.
func T(format string, args ...any) string {
	if Lang() == "zh" {
		if s, ok := zh[format]; ok {
			return fmt.Sprintf(s, args...)
		}
	}
	return fmt.Sprintf(format, args...)
}

// zh maps an English message to its Chinese rendering. Verbs (%s, %d) must be
// preserved in the same order.
var zh = map[string]string{
	// scan: header
	"capbus scan - read only, nothing has been changed":                              "capbus 体检 - 只读，没有改动任何东西",
	"%d plugin package(s) in agent profiles, %d resolvable MCP server(s) among them": "%d 个插件包，其中 %d 个是可识别的 MCP 服务",
	"%d capability(ies) declared, %d enabled":                                        "%d 个能力已声明，%d 个已开启",
	"agents: %s":            "已检测到的 Agent: %s",
	"agents: none detected": "没有检测到 Agent",
	"nothing to report":     "没有问题，一切正常",
	"%d finding(s)":         "发现 %d 个问题",

	// scan: natives
	"%d MCP server(s) are installed inside an agent and cost tokens capbus cannot control":                                                    "%d 个 MCP 服务装在 Agent 内部，它们消耗的 token capbus 管不着",
	"Each one injects its tool descriptions into the model on every turn. Disabling them in capbus does nothing until capbus owns the entry.": "每一个都会把工具说明注入模型的每一轮对话。在 capbus 里关掉它们没有用，除非 capbus 接管了这条配置。",
	"capbus import  (then adopt: stop the plugin, install the capbus bundle)":                                                                 "capbus import（然后接管：停用原插件，装入 capbus 的条目）",
	"%d capability(ies) are installed twice: once natively, once in capbus":                                                                   "%d 个能力装了两份：Agent 原生一份，capbus 一份",
	"The native copy is what the agent actually loads, so the capbus switch has no effect yet.":                                               "Agent 实际加载的是原生那份，所以 capbus 的开关现在不起作用。",
	"capbus sync, then remove the native plugin":                                                                                              "先 capbus sync，再移除原生插件",
	"%d MCP plugin(s) cannot be adopted automatically":                                                                                        "%d 个 MCP 插件无法自动接管",
	"Their launch command is built at runtime, so capbus cannot copy it. They need a hand-written manifest.":                                  "它们的启动命令是运行时算出来的，capbus 抄不到。需要手写清单。",
	"write a manifest by hand for these, or leave them alone":                                                                                 "手写清单，或者放着不管",
	" - also declared in capbus; the agent still loads its own copy":                                                                          " - 也在 capbus 里声明了；但 Agent 加载的还是它自己那份",
	" - its bundle could not be read":                                                                                                         " - 它的 bundle 读不出来",
	" - launch command is computed, no plain command to copy":                                                                                 " - 启动命令是算出来的，没有可抄的明文命令",

	// scan: stateful
	"%d shared capability(ies) look stateful": "%d 个被共享的能力看起来是有状态的",
	"Two agents driving one live session overwrite each other's target, cursor or open document, and nothing reports an error. Isolation - one process per agent - is the fix, not a lock.": "两个 Agent 操作同一个会话会互相覆盖对方的目标、光标或打开的文件，而且不会报错。正确做法是隔离（每个 Agent 一份进程），不是加锁。",
	"%s exposes %s-style tools but is declared scope: global": "%s 提供了 %s 这类工具，却被声明成 scope: global（全机共享）",
	"re-declare with scope: workspace, then capbus sync":      "改声明为 scope: workspace，然后 capbus sync",

	// scan: runtime
	"%d enabled capability(ies) cannot start: missing runtime":                                     "%d 个已开启的能力起不来：缺运行时",
	"A capability that cannot start is still declared, so your agents may report a broken plugin.": "能力起不来但仍在声明里，你的 Agent 可能会报插件损坏。",
	"install the missing runtime, or disable the capability":                                       "安装缺少的运行时，或者把这个能力关掉",
	"%s needs %q, which is not installed":                                                          "%s 需要 %q，但系统里没有",

	// scan: secrets
	"the credential store cannot be opened": "凭据库打不开",
	"%s needs %s":                           "%s 需要 %s",
	"%d credential(s) are missing":          "缺 %d 个凭据",
	"These capabilities will fail at startup. The value never needs to pass through an agent.": "这些能力启动时会失败。密钥的值不需要经过 Agent。",
	"capbus secret set <NAME>": "capbus secret set <名称>",

	// scan: stale
	"%d stale entr(ies) left in agent configs":                          "%d 条残留配置留在 Agent 里",
	"%s is still configured in an agent but no longer declared":         "%s 还在 Agent 配置里，但已经不再声明",
	"capbus writes the whole capability block, so a sync removes them.": "capbus 写的是整块能力区，跑一次 sync 就会清掉。",
	"capbus sync": "capbus sync",

	// scan: cost
	"no capability costs have been measured yet":          "还没有测量过任何能力的开销",
	"Without a measurement the per-turn cost is unknown.": "没测量就不知道每轮要花多少 token。",
	"capbus cost":                  "capbus cost",
	"the enabled set is expensive": "当前开启的能力很贵",
	"%d enabled capability(ies) cost about %d tokens on every turn": "%d 个已开启的能力，每轮约 %d tokens",
	", and %d are unmeasured":                                "，另有 %d 个还没测量",
	"capbus use <name>  (keep only what this session needs)": "capbus use <名称>（只留这次要用的）",

	// scan: supervisor
	"%d global capabilities will each start a separate copy in every agent": "%d 个全局能力会在每个 Agent 里各起一份副本",
	"One shared backend would start once and serve every agent.":            "用共享后端的话，只启动一次，服务所有 Agent。",
	"capbus supervise, then capbus sync --shared":                           "capbus supervise，然后 capbus sync --shared",

	// tables
	"NAME":                 "名称",
	"KIND":                 "类型",
	"SCOPE":                "作用域",
	"STATEFUL":             "有状态",
	"SOURCE":               "来源",
	"TOOLS":                "工具数",
	"BYTES":                "字节",
	"TOKENS":               "tokens",
	"STATE":                "状态",
	"CALLS":                "调用次数",
	"LAST CALL":            "最近调用",
	"TOP TOOL":             "最常用工具",
	"BYTES OUT":            "返回字节",
	"TRIMMED":              "已裁剪",
	"CAPABILITY":           "能力",
	"nothing recorded yet": "还没有记录",
	"calls are recorded when an agent runs a capability through capbus exec, or through the shared backend": "只有 Agent 通过 capbus exec 或共享后端调用能力时，才会被记录",
	"%d event(s) from the last %d of %s": "共 %d 条记录（最近 %d 条），日志文件 %s",

	// list / cost / sync
	"no capabilities declared":                   "没有声明任何能力",
	"add one to %s":                              "在 %s 里加一个",
	"probe failed: ":                             "探测失败: ",
	"~%d tokens on every turn, over %d measured": "每轮约 %d tokens（已测量 %d 个）",
	" (+%d failed to probe)":                     "；%d 个探测失败",
	"that is a large slice of the context window: `capbus use <name>` narrows it": "这占上下文窗口很大一块：用 `capbus use <名称>` 收窄",
	"wrote": "已写入",
	"no agent config found - install an agent, or run `capbus doctor`": "没找到任何 Agent 的配置——先装一个 Agent，或运行 `capbus doctor`",
	"%d capability(ies) injected":                                      "已注入 %d 个能力",
	", about %d tokens on every turn":                                  "，每轮约 %d tokens",
	" (%d unmeasured: run `capbus cost`)":                              "（%d 个未测量：运行 `capbus cost`）",
	"%d of %d capability(ies) will be injected":                        "%d / %d 个能力将被注入",
	"run `capbus sync` to apply":                                       "运行 `capbus sync` 生效",
	"on":                                                               "开",
	"off":                                                              "关",
	"[off]":                                                            "[关]",
	"recording tool calls (metadata only) to":                          "正在记录工具调用（只记元数据）到",
	"clear it any time with: capbus activity --clear":                  "随时可用 capbus activity --clear 清空",
	"activity log cleared":                                             "调用记录已清空",
	"action log cleared":                                               "动作记录已清空",
	"capbus has not changed anything yet":                              "capbus 还没有改动过任何东西",
	"log file: ":                                                       "记录文件：",
	"dry run: nothing was written; drop --dry-run to apply":            "演练：没有写入任何文件；去掉 --dry-run 才会真的写",
	"already up to date: every agent already matches the manifest":                                                     "已是最新：每个 Agent 的配置都与清单一致",
	"refusing to sync: fix the manifest error(s) above first; nothing was written":                                     "拒绝同步：先修上面的清单错误，本次没有写入任何文件",
	"name collision(s): these capabilities were NOT written":                                                           "名称冲突：下面这些能力没有被写入",
	"capbus supervise is experimental and needs --experimental":                                                        "capbus supervise 是实验性功能，必须加 --experimental",
	"A shared backend multiplexes several agents over one stdio process. That has costs you cannot see:":               "共享后端把多个 Agent 复用到同一个 stdio 进程上，这会带来你看不见的代价：",
	"  - the backend handshakes once, so it never learns each agent's capabilities":                                    "  - 后端只握手一次，永远不知道每个 Agent 各自声明了什么能力",
	"  - a server asking its client (sampling, roots, elicitation) gets an error, not an answer":                       "  - 服务端反过来问客户端（sampling / roots / elicitation）时只能收到错误，得不到答案",
	"  - notifications are delivered to every attached agent":                                                          "  - 通知会发给每一个连上来的 Agent",
	"Use it for tools-only capabilities. Anything that needs its own session should stay unshared":                     "只对「纯工具」类能力用它。任何需要自己会话的能力都不要共享",
	"(scope: workspace). Run `capbus supervise --experimental` to accept these terms.":                                 "（用 scope: workspace）。确认接受这些代价后，运行 `capbus supervise --experimental`。",
	"warning: shared mode is experimental; tools-only capabilities only, and notifications reach every attached agent": "注意：共享模式是实验性的；只适合纯工具类能力，且通知会送到每个接入的 Agent",
}
