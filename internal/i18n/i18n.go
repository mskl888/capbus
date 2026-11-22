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
}
