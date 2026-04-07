package i18n

var messagesCN = map[string]string{
	// Language selection
	"lang.prompt": "Select language / 选择语言:",
	"lang.en":     "1) English",
	"lang.cn":     "2) 中文",
	"lang.ask":    "输入 1 或 2: ",
	"lang.saved":  "语言已设置为中文。",

	// TUI placeholders & hints
	"tui.placeholder.default":  "输入消息或 /命令...",
	"tui.placeholder.approval": "审批：y 同意 / n 拒绝 / a 始终允许",
	"tui.placeholder.busy":     "正在执行，你可以继续输入...",
	"tui.hint.approval":        "等待你的选择",
	"tui.hint.busy":            "当前任务运行中",
	"tui.hint.send":            "",

	// TUI status
	"tui.status.ready":          "就绪",
	"tui.status.running":        "运行中",
	"tui.status.error":          "出错",
	"tui.status.approval":       "需要审批",
	"tui.status.drawer.shown":   "活动抽屉已展开",
	"tui.status.drawer.hidden":  "活动抽屉已收起",
	"tui.status.filter.updated": "日志过滤已切换",
	"tui.status.bg.update":      "后台任务更新",

	// TUI labels
	"tui.label.messages":    "%d 条消息",
	"tui.label.plan":        "计划",
	"tui.label.activity":    "活动  %s  %d",
	"tui.label.no.activity": "还没有工具活动。",
	"tui.label.no.match":    "没有匹配的活动。",

	// Approval inline
	"tui.approval.command": "需要执行命令",
	"tui.approval.write":   "需要写入文件",
	"tui.approval.general": "需要你的确认",
	"tui.approval.hint":    "输入 y 同意，n 拒绝，a 始终允许",
	"tui.approval.waiting": "审批仍在等待，输入 y 同意，n 拒绝，a 始终允许",
	"tui.approval.granted": "已批准",
	"tui.approval.denied":  "已拒绝",
	"tui.approval.always":  "已批准，审批模式切换为 dangerously",
	"tui.approval.mode":    "审批模式切换为 dangerously",
	"tui.approval.bar":     "[y] 同意   [n] 拒绝   [a] 始终允许",

	// Terminal approval
	"approval.cmd.header":     "需要确认执行命令：",
	"approval.cmd.prompt":     "执行？[y] 同意 / [n] 拒绝 / [a] 本次会话始终允许: ",
	"approval.write.header":   "需要确认写入文件：",
	"approval.write.prompt":   "写入？[y] 同意 / [n] 拒绝 / [a] 本次会话始终允许: ",
	"approval.switched":       "本次会话审批模式已切换为 dangerously",
	"approval.invalid":        "请输入 y、n 或 a",
	"approval.cmd.need.tty":   "命令审批需要交互终端；使用 --dangerously 可跳过确认",
	"approval.write.need.tty": "文件写入审批需要交互终端；使用 --dangerously 可跳过确认",

	// /help
	"help": `命令：
  /help                 显示此帮助
  /exit, /quit          退出
  /reset                清除对话上下文
  /session [name|new]   切换或新建会话
  /session save         保存当前会话状态
  /session restore      恢复当前会话状态
  /status               显示当前状态
  /plan                 显示 docs/plan.md
  /compact              立即压缩上下文
  /agents               查看或管理后台子代理
  /hooks                查看或调整 hooks 配置
  /mcp                  管理 MCP 服务与资源
  /plugin               列出或加载插件
  /reload-plugins       重新加载已加载插件
  /skills               列出已发现技能
  /scope                显示项目作用域
  /model <name>         切换模型
  /policy ...           查看或调整持久化工具策略
  /review [base] [target] [--staged] [--path <path>]
                        审查当前 git diff，可带范围参数
  /approval <mode>      设置审批模式 (confirm|dangerously)
  /memory               查看或管理记忆
  /memory team ...      查看或管理团队记忆
  /remember <text>      保存项目记忆
  /remember-global <t>  保存全局记忆
  /forget <query>       删除匹配的项目记忆
  /forget-global <q>    删除匹配的全局记忆
  /memorize             从对话中提取记忆
  /bg <task>            启动后台任务
  /bg-role <r> <task>   启动角色化后台任务
  /jobs                 列出后台任务
  /job <id>             查看后台任务详情
  /job-send <id> <msg>  向后台任务追加指令
  /job-cancel <id>      取消后台任务
  /job-apply <id>       将后台结果注入对话
  /audit stats          显示工具审计摘要
  /audit tail [n]       显示最近工具审计记录
  /audit errors [n]     显示最近失败的审计记录
  /debug-tool-call      显示最近一次工具调用详情
  /debug-tool-call tail [n]
                        显示最近几次工具调用的详细信息
  /debug-tool-call errors [n]
                        显示最近失败工具调用的详细信息
  /debug-tool-call replay
                        重放最近一次已审计的工具调用

直接输入自然语言即可，大多数情况不需要命令。`,

	// Commands
	"cmd.reset":              "上下文已重置",
	"cmd.approval.usage":     "用法：/approval confirm|dangerously",
	"cmd.approval.set":       "审批模式已设为 %s",
	"cmd.output.deprecated":  "output 命令已弃用；chat 默认使用终端渲染",
	"cmd.model.usage":        "用法：/model <名称>",
	"cmd.model.set":          "模型已切换为 %s",
	"cmd.bg.started":         "后台任务已启动 %s",
	"cmd.bgrole.usage":       "用法：/bg-role <general|explore|plan|implement|verify> <任务>",
	"cmd.job.usage":          "用法：/job <id>",
	"cmd.job.unknown":        "未知任务 %q",
	"cmd.jobsend.usage":      "用法：/job-send <id> <内容>",
	"cmd.jobsend.ok":         "已追加指令到 %s",
	"cmd.jobcancel.usage":    "用法：/job-cancel <id>",
	"cmd.jobcancel.ok":       "已取消 %s",
	"cmd.jobapply.usage":     "用法：/job-apply <id>",
	"cmd.jobapply.ok":        "已将 %s 的结果注入对话上下文",
	"cmd.audit.usage.stats":  "用法：/audit stats",
	"cmd.audit.usage.tail":   "用法：/audit tail [n]",
	"cmd.audit.usage.errors": "用法：/audit errors [n]",
	"cmd.debugtool.usage":    "用法：/debug-tool-call [tail|errors [n]|replay]",
	"cmd.audit.empty":        "还没有审计记录",
	"cmd.audit.no_errors":    "最近窗口内没有失败审计记录",
	"cmd.remember.usage":     "用法：/remember <内容>",
	"cmd.remember.ok":        "项目记忆已保存",
	"cmd.rememberg.usage":    "用法：/remember-global <内容>",
	"cmd.rememberg.ok":       "全局记忆已保存",
	"cmd.forget.usage":       "用法：/forget <关键词>",
	"cmd.forget.ok":          "已删除 %d 条项目记忆",
	"cmd.forgetg.usage":      "用法：/forget-global <关键词>",
	"cmd.forgetg.ok":         "已删除 %d 条全局记忆",
	"cmd.memorize.err":       "记忆提取出错：%v",
	"cmd.memorize.ok":        "已添加 %d 条记忆",
	"cmd.session.already":    "当前已在会话 %s",
	"cmd.session.switched":   "已切换到会话 %s",
	"cmd.session.started":    "已新建会话 %s",

	// Memory responses
	"mem.reject":               "这条内容不适合记成长期记忆；如果你希望记住稳定偏好或项目事实，请直接说明。",
	"mem.saved.global":         "已记住为全局偏好。",
	"mem.saved.project":        "已记住为当前项目记忆。",
	"mem.no.global.delete":     "没有可删除的全局记忆。",
	"mem.no.project.delete":    "没有可删除的项目记忆。",
	"mem.no.delete":            "没有可删除的记忆。",
	"mem.deleted.last.global":  "已删除最近一条全局记忆。",
	"mem.deleted.last.project": "已删除最近一条项目记忆。",
	"mem.forget.what":          "请明确要忘掉什么。",
	"mem.no.match":             "没有找到匹配的记忆。",
	"mem.deleted.mixed":        "已删除 %d 条记忆。",
	"mem.deleted.global":       "已删除 %d 条全局记忆。",
	"mem.deleted.project":      "已删除 %d 条项目记忆。",

	// Usage
	"usage.header":       "用法：",
	"usage.examples":     "示例：",
	"usage.error.config": "配置无效：%v\n",
	"usage.error.task":   "缺少任务",
	"usage.error.agent":  "agent 出错：%v\n",
	"usage.error.chat":   "chat 初始化出错：%v\n",
	"usage.error.ui":     "chat UI 出错：%v\n",
	"auto.memory.err":    "自动记忆出错：%v\n",
	"auto.memory.ok":     "自动记忆 %d 条\n",
}
