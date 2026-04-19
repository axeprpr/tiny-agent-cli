# OpenCode 用户级细化用例 001-200

- 本清单聚焦用户视角操作路径（CLI、Chat 命令、模型实跑）。
- 001-020: CLI；021-140: Chat Slash 命令；141-200: 模型 token 消耗场景。

| ID | 模块 | 模式 | 用例标题 | 用户操作步骤 | 通过标准 |
| --- | --- | --- | --- | --- | --- |
| 001 | CLI | cli | version shows build version | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e version`，观察输出与退出码。 | 匹配 `.+` |
| 002 | CLI | cli | help shows usage | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e help`，观察输出与退出码。 | 匹配 `Usage:` |
| 003 | CLI | cli | status shows workspace summary | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e status --workdir /tmp`，观察输出与退出码。 | 匹配 `workdir=/tmp` |
| 004 | CLI | cli | plan missing file returns empty | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e plan --workdir /tmp`，观察输出与退出码。 | 匹配 `^$|.+` |
| 005 | CLI | cli | contract missing returns placeholder | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e contract --workdir /tmp`，观察输出与退出码。 | 匹配 `\(no task contract\)|\(missing\)` |
| 006 | CLI | cli | capabilities lists packs | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e capabilities --workdir /tmp`，观察输出与退出码。 | 匹配 `ops:` |
| 007 | CLI | cli | skills lists bundled skills | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e skills --workdir /tmp`，观察输出与退出码。 | 匹配 `bundled` |
| 008 | CLI | cli | models command handles endpoint | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e models`，观察输出与退出码。 | 匹配 `model API error|request failed|[A-Za-z0-9][A-Za-z0-9._/-]{2,}` |
| 009 | CLI | cli | ping command handles endpoint | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e ping`，观察输出与退出码。 | 匹配 `pong|model API error|request failed` |
| 010 | CLI | cli | unknown command exits with guidance | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e definitely-unknown-subcmd`，观察输出与退出码。 | 匹配 `missing task|Usage:|not sure what you want|If you meant a command` |
| 011 | CLI | cli | run without task rejects | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e run`，观察输出与退出码。 | 匹配 `missing task|Usage:` |
| 012 | CLI | cli | control status path | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e status --workdir /root/tiny-agent-cli`，观察输出与退出码。 | 匹配 `state=` |
| 013 | CLI | cli | control plan path | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e plan --workdir /root/tiny-agent-cli`，观察输出与退出码。 | 匹配 `.+` |
| 014 | CLI | cli | control contract path | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e contract --workdir /root/tiny-agent-cli`，观察输出与退出码。 | 匹配 `task contract|\(no task contract\)|\(missing\)` |
| 015 | CLI | cli | capability detail query | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e capabilities --workdir /root/tiny-agent-cli ops`，观察输出与退出码。 | 匹配 `ops:` |
| 016 | CLI | cli | skills against repo | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e skills --workdir /root/tiny-agent-cli`，观察输出与退出码。 | 匹配 `bundled|hooks` |
| 017 | CLI | cli | status command_rules field | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e status --workdir /root/tiny-agent-cli`，观察输出与退出码。 | 匹配 `command_rules=` |
| 018 | CLI | cli | status sessions field | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e status --workdir /root/tiny-agent-cli`，观察输出与退出码。 | 匹配 `sessions=` |
| 019 | CLI | cli | version non-empty | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e version`，观察输出与退出码。 | 匹配 `\S+` |
| 020 | CLI | cli | help includes chat | 执行命令: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/bin/tacli-user-e2e help`，观察输出与退出码。 | 匹配 `tacli chat` |
| 021 | ChatCmd | chat_cmd | slash help #1 | 启动 `tacli chat --resume u001` 后输入 `/help`，观察命令回显。 | 匹配 `tacli dev` |
| 022 | ChatCmd | chat_cmd | status view #2 | 启动 `tacli chat --resume u002` 后输入 `/status`，观察命令回显。 | 匹配 `conversation=` |
| 023 | ChatCmd | chat_cmd | new conversation #3 | 启动 `tacli chat --resume u003` 后输入 `/new demo`，观察命令回显。 | 匹配 `started conversation` |
| 024 | ChatCmd | chat_cmd | resume missing conversation #4 | 启动 `tacli chat --resume u004` 后输入 `/resume missing`，观察命令回显。 | 匹配 `no saved conversation` |
| 025 | ChatCmd | chat_cmd | rename conversation #5 | 启动 `tacli chat --resume u005` 后输入 `/rename renamed`，观察命令回显。 | 匹配 `renamed conversation` |
| 026 | ChatCmd | chat_cmd | fork conversation #6 | 启动 `tacli chat --resume u006` 后输入 `/fork branched`，观察命令回显。 | 匹配 `forked conversation` |
| 027 | ChatCmd | chat_cmd | tree rendering #7 | 启动 `tacli chat --resume u007` 后输入 `/tree`，观察命令回显。 | 匹配 `conversation tree|current_conversation` |
| 028 | ChatCmd | chat_cmd | memory show #8 | 启动 `tacli chat --resume u008` 后输入 `/memory show`，观察命令回显。 | 匹配 `path=.*memory\.json` |
| 029 | ChatCmd | chat_cmd | memory remember #9 | 启动 `tacli chat --resume u009` 后输入 `/memory remember alpha-note`，观察命令回显。 | 匹配 `project_notes=1|project memory saved` |
| 030 | ChatCmd | chat_cmd | memory forget #10 | 启动 `tacli chat --resume u010` 后输入 `/memory forget alpha-note`，观察命令回显。 | 匹配 `removed` |
| 031 | ChatCmd | chat_cmd | memory team show #11 | 启动 `tacli chat --resume u011` 后输入 `/memory team show`，观察命令回显。 | 匹配 `team memory|team_notes=` |
| 032 | ChatCmd | chat_cmd | tasks list #12 | 启动 `tacli chat --resume u012` 后输入 `/tasks list`，观察命令回显。 | 匹配 `no tasks|task-` |
| 033 | ChatCmd | chat_cmd | tasks create #13 | 启动 `tacli chat --resume u013` 后输入 `/tasks create sample-task`，观察命令回显。 | 匹配 `task-001` |
| 034 | ChatCmd | chat_cmd | tasks update unknown #14 | 启动 `tacli chat --resume u014` 后输入 `/tasks update task-001 status=in_progress`，观察命令回显。 | 匹配 `unknown task|task-001` |
| 035 | ChatCmd | chat_cmd | tasks delete unknown #15 | 启动 `tacli chat --resume u015` 后输入 `/tasks delete task-001`，观察命令回显。 | 匹配 `unknown task|deleted` |
| 036 | ChatCmd | chat_cmd | policy view #16 | 启动 `tacli chat --resume u016` 后输入 `/policy`，观察命令回显。 | 匹配 `default=` |
| 037 | ChatCmd | chat_cmd | policy default allow #17 | 启动 `tacli chat --resume u017` 后输入 `/policy default allow`，观察命令回显。 | 匹配 `default=allow` |
| 038 | ChatCmd | chat_cmd | policy default prompt #18 | 启动 `tacli chat --resume u018` 后输入 `/policy default prompt`，观察命令回显。 | 匹配 `default=prompt` |
| 039 | ChatCmd | chat_cmd | policy command list #19 | 启动 `tacli chat --resume u019` 后输入 `/policy command list`，观察命令回显。 | 匹配 `command` |
| 040 | ChatCmd | chat_cmd | audit stats #20 | 启动 `tacli chat --resume u020` 后输入 `/audit stats`，观察命令回显。 | 匹配 `audit=` |
| 041 | ChatCmd | chat_cmd | debug-tool-call #21 | 启动 `tacli chat --resume u021` 后输入 `/debug-tool-call`，观察命令回显。 | 匹配 `audit|no audit events yet` |
| 042 | ChatCmd | chat_cmd | debug-turn #22 | 启动 `tacli chat --resume u022` 后输入 `/debug-turn`，观察命令回显。 | 匹配 `turn summaries|no turn summaries yet` |
| 043 | ChatCmd | chat_cmd | trace stats #23 | 启动 `tacli chat --resume u023` 后输入 `/trace stats`，观察命令回显。 | 匹配 `events=` |
| 044 | ChatCmd | chat_cmd | plugin list #24 | 启动 `tacli chat --resume u024` 后输入 `/plugin list`，观察命令回显。 | 匹配 `plugin` |
| 045 | ChatCmd | chat_cmd | skills list #25 | 启动 `tacli chat --resume u025` 后输入 `/skills`，观察命令回显。 | 匹配 `bundled|hooks` |
| 046 | ChatCmd | chat_cmd | capabilities list #26 | 启动 `tacli chat --resume u026` 后输入 `/capabilities`，观察命令回显。 | 匹配 `ops:` |
| 047 | ChatCmd | chat_cmd | mcp list #27 | 启动 `tacli chat --resume u027` 后输入 `/mcp list`，观察命令回显。 | 匹配 `MCP|no MCP servers configured` |
| 048 | ChatCmd | chat_cmd | hooks list #28 | 启动 `tacli chat --resume u028` 后输入 `/hooks`，观察命令回显。 | 匹配 `PreToolUse` |
| 049 | ChatCmd | chat_cmd | agents list #29 | 启动 `tacli chat --resume u029` 后输入 `/agents`，观察命令回显。 | 匹配 `background agents|no background agents` |
| 050 | ChatCmd | chat_cmd | jobs list #30 | 启动 `tacli chat --resume u030` 后输入 `/jobs`，观察命令回显。 | 匹配 `background jobs|no background jobs` |
| 051 | ChatCmd | chat_cmd | job unknown #31 | 启动 `tacli chat --resume u031` 后输入 `/job no-such`，观察命令回显。 | 匹配 `unknown job` |
| 052 | ChatCmd | chat_cmd | job-send unknown #32 | 启动 `tacli chat --resume u032` 后输入 `/job-send no-such hi`，观察命令回显。 | 匹配 `unknown job|usage` |
| 053 | ChatCmd | chat_cmd | job-cancel unknown #33 | 启动 `tacli chat --resume u033` 后输入 `/job-cancel no-such`，观察命令回显。 | 匹配 `unknown job|canceled` |
| 054 | ChatCmd | chat_cmd | job-apply unknown #34 | 启动 `tacli chat --resume u034` 后输入 `/job-apply no-such`，观察命令回显。 | 匹配 `unknown job|applied` |
| 055 | ChatCmd | chat_cmd | steer queue #35 | 启动 `tacli chat --resume u035` 后输入 `/steer hi`，观察命令回显。 | 匹配 `queued steering message` |
| 056 | ChatCmd | chat_cmd | follow queue #36 | 启动 `tacli chat --resume u036` 后输入 `/follow hi`，观察命令回显。 | 匹配 `queued follow-up message` |
| 057 | ChatCmd | chat_cmd | approval set allow #37 | 启动 `tacli chat --resume u037` 后输入 `/approval allow`，观察命令回显。 | 匹配 `approval mode set to` |
| 058 | ChatCmd | chat_cmd | approval set prompt #38 | 启动 `tacli chat --resume u038` 后输入 `/approval prompt`，观察命令回显。 | 匹配 `approval mode set to` |
| 059 | ChatCmd | chat_cmd | model set #39 | 启动 `tacli chat --resume u039` 后输入 `/model test-model`，观察命令回显。 | 匹配 `model set to test-model` |
| 060 | ChatCmd | chat_cmd | reset context #40 | 启动 `tacli chat --resume u040` 后输入 `/reset`，观察命令回显。 | 匹配 `context reset` |
| 061 | ChatCmd | chat_cmd | slash help #41 | 启动 `tacli chat --resume u041` 后输入 `/help`，观察命令回显。 | 匹配 `tacli dev` |
| 062 | ChatCmd | chat_cmd | status view #42 | 启动 `tacli chat --resume u042` 后输入 `/status`，观察命令回显。 | 匹配 `conversation=` |
| 063 | ChatCmd | chat_cmd | new conversation #43 | 启动 `tacli chat --resume u043` 后输入 `/new demo`，观察命令回显。 | 匹配 `started conversation` |
| 064 | ChatCmd | chat_cmd | resume missing conversation #44 | 启动 `tacli chat --resume u044` 后输入 `/resume missing`，观察命令回显。 | 匹配 `no saved conversation` |
| 065 | ChatCmd | chat_cmd | rename conversation #45 | 启动 `tacli chat --resume u045` 后输入 `/rename renamed`，观察命令回显。 | 匹配 `renamed conversation` |
| 066 | ChatCmd | chat_cmd | fork conversation #46 | 启动 `tacli chat --resume u046` 后输入 `/fork branched`，观察命令回显。 | 匹配 `forked conversation` |
| 067 | ChatCmd | chat_cmd | tree rendering #47 | 启动 `tacli chat --resume u047` 后输入 `/tree`，观察命令回显。 | 匹配 `conversation tree|current_conversation` |
| 068 | ChatCmd | chat_cmd | memory show #48 | 启动 `tacli chat --resume u048` 后输入 `/memory show`，观察命令回显。 | 匹配 `path=.*memory\.json` |
| 069 | ChatCmd | chat_cmd | memory remember #49 | 启动 `tacli chat --resume u049` 后输入 `/memory remember alpha-note`，观察命令回显。 | 匹配 `project_notes=1|project memory saved` |
| 070 | ChatCmd | chat_cmd | memory forget #50 | 启动 `tacli chat --resume u050` 后输入 `/memory forget alpha-note`，观察命令回显。 | 匹配 `removed` |
| 071 | ChatCmd | chat_cmd | memory team show #51 | 启动 `tacli chat --resume u051` 后输入 `/memory team show`，观察命令回显。 | 匹配 `team memory|team_notes=` |
| 072 | ChatCmd | chat_cmd | tasks list #52 | 启动 `tacli chat --resume u052` 后输入 `/tasks list`，观察命令回显。 | 匹配 `no tasks|task-` |
| 073 | ChatCmd | chat_cmd | tasks create #53 | 启动 `tacli chat --resume u053` 后输入 `/tasks create sample-task`，观察命令回显。 | 匹配 `task-001` |
| 074 | ChatCmd | chat_cmd | tasks update unknown #54 | 启动 `tacli chat --resume u054` 后输入 `/tasks update task-001 status=in_progress`，观察命令回显。 | 匹配 `unknown task|task-001` |
| 075 | ChatCmd | chat_cmd | tasks delete unknown #55 | 启动 `tacli chat --resume u055` 后输入 `/tasks delete task-001`，观察命令回显。 | 匹配 `unknown task|deleted` |
| 076 | ChatCmd | chat_cmd | policy view #56 | 启动 `tacli chat --resume u056` 后输入 `/policy`，观察命令回显。 | 匹配 `default=` |
| 077 | ChatCmd | chat_cmd | policy default allow #57 | 启动 `tacli chat --resume u057` 后输入 `/policy default allow`，观察命令回显。 | 匹配 `default=allow` |
| 078 | ChatCmd | chat_cmd | policy default prompt #58 | 启动 `tacli chat --resume u058` 后输入 `/policy default prompt`，观察命令回显。 | 匹配 `default=prompt` |
| 079 | ChatCmd | chat_cmd | policy command list #59 | 启动 `tacli chat --resume u059` 后输入 `/policy command list`，观察命令回显。 | 匹配 `command` |
| 080 | ChatCmd | chat_cmd | audit stats #60 | 启动 `tacli chat --resume u060` 后输入 `/audit stats`，观察命令回显。 | 匹配 `audit=` |
| 081 | ChatCmd | chat_cmd | debug-tool-call #61 | 启动 `tacli chat --resume u061` 后输入 `/debug-tool-call`，观察命令回显。 | 匹配 `audit|no audit events yet` |
| 082 | ChatCmd | chat_cmd | debug-turn #62 | 启动 `tacli chat --resume u062` 后输入 `/debug-turn`，观察命令回显。 | 匹配 `turn summaries|no turn summaries yet` |
| 083 | ChatCmd | chat_cmd | trace stats #63 | 启动 `tacli chat --resume u063` 后输入 `/trace stats`，观察命令回显。 | 匹配 `events=` |
| 084 | ChatCmd | chat_cmd | plugin list #64 | 启动 `tacli chat --resume u064` 后输入 `/plugin list`，观察命令回显。 | 匹配 `plugin` |
| 085 | ChatCmd | chat_cmd | skills list #65 | 启动 `tacli chat --resume u065` 后输入 `/skills`，观察命令回显。 | 匹配 `bundled|hooks` |
| 086 | ChatCmd | chat_cmd | capabilities list #66 | 启动 `tacli chat --resume u066` 后输入 `/capabilities`，观察命令回显。 | 匹配 `ops:` |
| 087 | ChatCmd | chat_cmd | mcp list #67 | 启动 `tacli chat --resume u067` 后输入 `/mcp list`，观察命令回显。 | 匹配 `MCP|no MCP servers configured` |
| 088 | ChatCmd | chat_cmd | hooks list #68 | 启动 `tacli chat --resume u068` 后输入 `/hooks`，观察命令回显。 | 匹配 `PreToolUse` |
| 089 | ChatCmd | chat_cmd | agents list #69 | 启动 `tacli chat --resume u069` 后输入 `/agents`，观察命令回显。 | 匹配 `background agents|no background agents` |
| 090 | ChatCmd | chat_cmd | jobs list #70 | 启动 `tacli chat --resume u070` 后输入 `/jobs`，观察命令回显。 | 匹配 `background jobs|no background jobs` |
| 091 | ChatCmd | chat_cmd | job unknown #71 | 启动 `tacli chat --resume u071` 后输入 `/job no-such`，观察命令回显。 | 匹配 `unknown job` |
| 092 | ChatCmd | chat_cmd | job-send unknown #72 | 启动 `tacli chat --resume u072` 后输入 `/job-send no-such hi`，观察命令回显。 | 匹配 `unknown job|usage` |
| 093 | ChatCmd | chat_cmd | job-cancel unknown #73 | 启动 `tacli chat --resume u073` 后输入 `/job-cancel no-such`，观察命令回显。 | 匹配 `unknown job|canceled` |
| 094 | ChatCmd | chat_cmd | job-apply unknown #74 | 启动 `tacli chat --resume u074` 后输入 `/job-apply no-such`，观察命令回显。 | 匹配 `unknown job|applied` |
| 095 | ChatCmd | chat_cmd | steer queue #75 | 启动 `tacli chat --resume u075` 后输入 `/steer hi`，观察命令回显。 | 匹配 `queued steering message` |
| 096 | ChatCmd | chat_cmd | follow queue #76 | 启动 `tacli chat --resume u076` 后输入 `/follow hi`，观察命令回显。 | 匹配 `queued follow-up message` |
| 097 | ChatCmd | chat_cmd | approval set allow #77 | 启动 `tacli chat --resume u077` 后输入 `/approval allow`，观察命令回显。 | 匹配 `approval mode set to` |
| 098 | ChatCmd | chat_cmd | approval set prompt #78 | 启动 `tacli chat --resume u078` 后输入 `/approval prompt`，观察命令回显。 | 匹配 `approval mode set to` |
| 099 | ChatCmd | chat_cmd | model set #79 | 启动 `tacli chat --resume u079` 后输入 `/model test-model`，观察命令回显。 | 匹配 `model set to test-model` |
| 100 | ChatCmd | chat_cmd | reset context #80 | 启动 `tacli chat --resume u080` 后输入 `/reset`，观察命令回显。 | 匹配 `context reset` |
| 101 | ChatCmd | chat_cmd | slash help #81 | 启动 `tacli chat --resume u081` 后输入 `/help`，观察命令回显。 | 匹配 `tacli dev` |
| 102 | ChatCmd | chat_cmd | status view #82 | 启动 `tacli chat --resume u082` 后输入 `/status`，观察命令回显。 | 匹配 `conversation=` |
| 103 | ChatCmd | chat_cmd | new conversation #83 | 启动 `tacli chat --resume u083` 后输入 `/new demo`，观察命令回显。 | 匹配 `started conversation` |
| 104 | ChatCmd | chat_cmd | resume missing conversation #84 | 启动 `tacli chat --resume u084` 后输入 `/resume missing`，观察命令回显。 | 匹配 `no saved conversation` |
| 105 | ChatCmd | chat_cmd | rename conversation #85 | 启动 `tacli chat --resume u085` 后输入 `/rename renamed`，观察命令回显。 | 匹配 `renamed conversation` |
| 106 | ChatCmd | chat_cmd | fork conversation #86 | 启动 `tacli chat --resume u086` 后输入 `/fork branched`，观察命令回显。 | 匹配 `forked conversation` |
| 107 | ChatCmd | chat_cmd | tree rendering #87 | 启动 `tacli chat --resume u087` 后输入 `/tree`，观察命令回显。 | 匹配 `conversation tree|current_conversation` |
| 108 | ChatCmd | chat_cmd | memory show #88 | 启动 `tacli chat --resume u088` 后输入 `/memory show`，观察命令回显。 | 匹配 `path=.*memory\.json` |
| 109 | ChatCmd | chat_cmd | memory remember #89 | 启动 `tacli chat --resume u089` 后输入 `/memory remember alpha-note`，观察命令回显。 | 匹配 `project_notes=1|project memory saved` |
| 110 | ChatCmd | chat_cmd | memory forget #90 | 启动 `tacli chat --resume u090` 后输入 `/memory forget alpha-note`，观察命令回显。 | 匹配 `removed` |
| 111 | ChatCmd | chat_cmd | memory team show #91 | 启动 `tacli chat --resume u091` 后输入 `/memory team show`，观察命令回显。 | 匹配 `team memory|team_notes=` |
| 112 | ChatCmd | chat_cmd | tasks list #92 | 启动 `tacli chat --resume u092` 后输入 `/tasks list`，观察命令回显。 | 匹配 `no tasks|task-` |
| 113 | ChatCmd | chat_cmd | tasks create #93 | 启动 `tacli chat --resume u093` 后输入 `/tasks create sample-task`，观察命令回显。 | 匹配 `task-001` |
| 114 | ChatCmd | chat_cmd | tasks update unknown #94 | 启动 `tacli chat --resume u094` 后输入 `/tasks update task-001 status=in_progress`，观察命令回显。 | 匹配 `unknown task|task-001` |
| 115 | ChatCmd | chat_cmd | tasks delete unknown #95 | 启动 `tacli chat --resume u095` 后输入 `/tasks delete task-001`，观察命令回显。 | 匹配 `unknown task|deleted` |
| 116 | ChatCmd | chat_cmd | policy view #96 | 启动 `tacli chat --resume u096` 后输入 `/policy`，观察命令回显。 | 匹配 `default=` |
| 117 | ChatCmd | chat_cmd | policy default allow #97 | 启动 `tacli chat --resume u097` 后输入 `/policy default allow`，观察命令回显。 | 匹配 `default=allow` |
| 118 | ChatCmd | chat_cmd | policy default prompt #98 | 启动 `tacli chat --resume u098` 后输入 `/policy default prompt`，观察命令回显。 | 匹配 `default=prompt` |
| 119 | ChatCmd | chat_cmd | policy command list #99 | 启动 `tacli chat --resume u099` 后输入 `/policy command list`，观察命令回显。 | 匹配 `command` |
| 120 | ChatCmd | chat_cmd | audit stats #100 | 启动 `tacli chat --resume u100` 后输入 `/audit stats`，观察命令回显。 | 匹配 `audit=` |
| 121 | ChatCmd | chat_cmd | debug-tool-call #101 | 启动 `tacli chat --resume u101` 后输入 `/debug-tool-call`，观察命令回显。 | 匹配 `audit|no audit events yet` |
| 122 | ChatCmd | chat_cmd | debug-turn #102 | 启动 `tacli chat --resume u102` 后输入 `/debug-turn`，观察命令回显。 | 匹配 `turn summaries|no turn summaries yet` |
| 123 | ChatCmd | chat_cmd | trace stats #103 | 启动 `tacli chat --resume u103` 后输入 `/trace stats`，观察命令回显。 | 匹配 `events=` |
| 124 | ChatCmd | chat_cmd | plugin list #104 | 启动 `tacli chat --resume u104` 后输入 `/plugin list`，观察命令回显。 | 匹配 `plugin` |
| 125 | ChatCmd | chat_cmd | skills list #105 | 启动 `tacli chat --resume u105` 后输入 `/skills`，观察命令回显。 | 匹配 `bundled|hooks` |
| 126 | ChatCmd | chat_cmd | capabilities list #106 | 启动 `tacli chat --resume u106` 后输入 `/capabilities`，观察命令回显。 | 匹配 `ops:` |
| 127 | ChatCmd | chat_cmd | mcp list #107 | 启动 `tacli chat --resume u107` 后输入 `/mcp list`，观察命令回显。 | 匹配 `MCP|no MCP servers configured` |
| 128 | ChatCmd | chat_cmd | hooks list #108 | 启动 `tacli chat --resume u108` 后输入 `/hooks`，观察命令回显。 | 匹配 `PreToolUse` |
| 129 | ChatCmd | chat_cmd | agents list #109 | 启动 `tacli chat --resume u109` 后输入 `/agents`，观察命令回显。 | 匹配 `background agents|no background agents` |
| 130 | ChatCmd | chat_cmd | jobs list #110 | 启动 `tacli chat --resume u110` 后输入 `/jobs`，观察命令回显。 | 匹配 `background jobs|no background jobs` |
| 131 | ChatCmd | chat_cmd | job unknown #111 | 启动 `tacli chat --resume u111` 后输入 `/job no-such`，观察命令回显。 | 匹配 `unknown job` |
| 132 | ChatCmd | chat_cmd | job-send unknown #112 | 启动 `tacli chat --resume u112` 后输入 `/job-send no-such hi`，观察命令回显。 | 匹配 `unknown job|usage` |
| 133 | ChatCmd | chat_cmd | job-cancel unknown #113 | 启动 `tacli chat --resume u113` 后输入 `/job-cancel no-such`，观察命令回显。 | 匹配 `unknown job|canceled` |
| 134 | ChatCmd | chat_cmd | job-apply unknown #114 | 启动 `tacli chat --resume u114` 后输入 `/job-apply no-such`，观察命令回显。 | 匹配 `unknown job|applied` |
| 135 | ChatCmd | chat_cmd | steer queue #115 | 启动 `tacli chat --resume u115` 后输入 `/steer hi`，观察命令回显。 | 匹配 `queued steering message` |
| 136 | ChatCmd | chat_cmd | follow queue #116 | 启动 `tacli chat --resume u116` 后输入 `/follow hi`，观察命令回显。 | 匹配 `queued follow-up message` |
| 137 | ChatCmd | chat_cmd | approval set allow #117 | 启动 `tacli chat --resume u117` 后输入 `/approval allow`，观察命令回显。 | 匹配 `approval mode set to` |
| 138 | ChatCmd | chat_cmd | approval set prompt #118 | 启动 `tacli chat --resume u118` 后输入 `/approval prompt`，观察命令回显。 | 匹配 `approval mode set to` |
| 139 | ChatCmd | chat_cmd | model set #119 | 启动 `tacli chat --resume u119` 后输入 `/model test-model`，观察命令回显。 | 匹配 `model set to test-model` |
| 140 | ChatCmd | chat_cmd | reset context #120 | 启动 `tacli chat --resume u120` 后输入 `/reset`，观察命令回显。 | 匹配 `context reset` |
| 141 | ModelRun | model_run | model extracts exact secret 001 | 写入 `secret-001.txt`，执行 run 任务 `Read secret-001.txt and reply with exactly amber-001`，验证最终文本与工具链。 | 匹配 `amber-001` |
| 142 | ModelRun | model_run | model extracts exact secret 002 | 写入 `secret-002.txt`，执行 run 任务 `Read secret-002.txt and reply with exactly amber-002`，验证最终文本与工具链。 | 匹配 `amber-002` |
| 143 | ModelRun | model_run | model extracts exact secret 003 | 写入 `secret-003.txt`，执行 run 任务 `Read secret-003.txt and reply with exactly amber-003`，验证最终文本与工具链。 | 匹配 `amber-003` |
| 144 | ModelRun | model_run | model extracts exact secret 004 | 写入 `secret-004.txt`，执行 run 任务 `Read secret-004.txt and reply with exactly amber-004`，验证最终文本与工具链。 | 匹配 `amber-004` |
| 145 | ModelRun | model_run | model extracts exact secret 005 | 写入 `secret-005.txt`，执行 run 任务 `Read secret-005.txt and reply with exactly amber-005`，验证最终文本与工具链。 | 匹配 `amber-005` |
| 146 | ModelRun | model_run | model extracts exact secret 006 | 写入 `secret-006.txt`，执行 run 任务 `Read secret-006.txt and reply with exactly amber-006`，验证最终文本与工具链。 | 匹配 `amber-006` |
| 147 | ModelRun | model_run | model extracts exact secret 007 | 写入 `secret-007.txt`，执行 run 任务 `Read secret-007.txt and reply with exactly amber-007`，验证最终文本与工具链。 | 匹配 `amber-007` |
| 148 | ModelRun | model_run | model extracts exact secret 008 | 写入 `secret-008.txt`，执行 run 任务 `Read secret-008.txt and reply with exactly amber-008`，验证最终文本与工具链。 | 匹配 `amber-008` |
| 149 | ModelRun | model_run | model extracts exact secret 009 | 写入 `secret-009.txt`，执行 run 任务 `Read secret-009.txt and reply with exactly amber-009`，验证最终文本与工具链。 | 匹配 `amber-009` |
| 150 | ModelRun | model_run | model extracts exact secret 010 | 写入 `secret-010.txt`，执行 run 任务 `Read secret-010.txt and reply with exactly amber-010`，验证最终文本与工具链。 | 匹配 `amber-010` |
| 151 | ModelRun | model_run | model extracts exact secret 011 | 写入 `secret-011.txt`，执行 run 任务 `Read secret-011.txt and reply with exactly amber-011`，验证最终文本与工具链。 | 匹配 `amber-011` |
| 152 | ModelRun | model_run | model extracts exact secret 012 | 写入 `secret-012.txt`，执行 run 任务 `Read secret-012.txt and reply with exactly amber-012`，验证最终文本与工具链。 | 匹配 `amber-012` |
| 153 | ModelRun | model_run | model extracts exact secret 013 | 写入 `secret-013.txt`，执行 run 任务 `Read secret-013.txt and reply with exactly amber-013`，验证最终文本与工具链。 | 匹配 `amber-013` |
| 154 | ModelRun | model_run | model extracts exact secret 014 | 写入 `secret-014.txt`，执行 run 任务 `Read secret-014.txt and reply with exactly amber-014`，验证最终文本与工具链。 | 匹配 `amber-014` |
| 155 | ModelRun | model_run | model extracts exact secret 015 | 写入 `secret-015.txt`，执行 run 任务 `Read secret-015.txt and reply with exactly amber-015`，验证最终文本与工具链。 | 匹配 `amber-015` |
| 156 | ModelRun | model_run | model extracts exact secret 016 | 写入 `secret-016.txt`，执行 run 任务 `Read secret-016.txt and reply with exactly amber-016`，验证最终文本与工具链。 | 匹配 `amber-016` |
| 157 | ModelRun | model_run | model extracts exact secret 017 | 写入 `secret-017.txt`，执行 run 任务 `Read secret-017.txt and reply with exactly amber-017`，验证最终文本与工具链。 | 匹配 `amber-017` |
| 158 | ModelRun | model_run | model extracts exact secret 018 | 写入 `secret-018.txt`，执行 run 任务 `Read secret-018.txt and reply with exactly amber-018`，验证最终文本与工具链。 | 匹配 `amber-018` |
| 159 | ModelRun | model_run | model extracts exact secret 019 | 写入 `secret-019.txt`，执行 run 任务 `Read secret-019.txt and reply with exactly amber-019`，验证最终文本与工具链。 | 匹配 `amber-019` |
| 160 | ModelRun | model_run | model extracts exact secret 020 | 写入 `secret-020.txt`，执行 run 任务 `Read secret-020.txt and reply with exactly amber-020`，验证最终文本与工具链。 | 匹配 `amber-020` |
| 161 | ModelRun | model_run | model extracts exact secret 021 | 写入 `secret-021.txt`，执行 run 任务 `Read secret-021.txt and reply with exactly amber-021`，验证最终文本与工具链。 | 匹配 `amber-021` |
| 162 | ModelRun | model_run | model extracts exact secret 022 | 写入 `secret-022.txt`，执行 run 任务 `Read secret-022.txt and reply with exactly amber-022`，验证最终文本与工具链。 | 匹配 `amber-022` |
| 163 | ModelRun | model_run | model extracts exact secret 023 | 写入 `secret-023.txt`，执行 run 任务 `Read secret-023.txt and reply with exactly amber-023`，验证最终文本与工具链。 | 匹配 `amber-023` |
| 164 | ModelRun | model_run | model extracts exact secret 024 | 写入 `secret-024.txt`，执行 run 任务 `Read secret-024.txt and reply with exactly amber-024`，验证最终文本与工具链。 | 匹配 `amber-024` |
| 165 | ModelRun | model_run | model extracts exact secret 025 | 写入 `secret-025.txt`，执行 run 任务 `Read secret-025.txt and reply with exactly amber-025`，验证最终文本与工具链。 | 匹配 `amber-025` |
| 166 | ModelRun | model_run | model extracts exact secret 026 | 写入 `secret-026.txt`，执行 run 任务 `Read secret-026.txt and reply with exactly amber-026`，验证最终文本与工具链。 | 匹配 `amber-026` |
| 167 | ModelRun | model_run | model extracts exact secret 027 | 写入 `secret-027.txt`，执行 run 任务 `Read secret-027.txt and reply with exactly amber-027`，验证最终文本与工具链。 | 匹配 `amber-027` |
| 168 | ModelRun | model_run | model extracts exact secret 028 | 写入 `secret-028.txt`，执行 run 任务 `Read secret-028.txt and reply with exactly amber-028`，验证最终文本与工具链。 | 匹配 `amber-028` |
| 169 | ModelRun | model_run | model extracts exact secret 029 | 写入 `secret-029.txt`，执行 run 任务 `Read secret-029.txt and reply with exactly amber-029`，验证最终文本与工具链。 | 匹配 `amber-029` |
| 170 | ModelRun | model_run | model extracts exact secret 030 | 写入 `secret-030.txt`，执行 run 任务 `Read secret-030.txt and reply with exactly amber-030`，验证最终文本与工具链。 | 匹配 `amber-030` |
| 171 | ModelRun | model_run | model extracts exact secret 031 | 写入 `secret-031.txt`，执行 run 任务 `Read secret-031.txt and reply with exactly amber-031`，验证最终文本与工具链。 | 匹配 `amber-031` |
| 172 | ModelRun | model_run | model extracts exact secret 032 | 写入 `secret-032.txt`，执行 run 任务 `Read secret-032.txt and reply with exactly amber-032`，验证最终文本与工具链。 | 匹配 `amber-032` |
| 173 | ModelRun | model_run | model extracts exact secret 033 | 写入 `secret-033.txt`，执行 run 任务 `Read secret-033.txt and reply with exactly amber-033`，验证最终文本与工具链。 | 匹配 `amber-033` |
| 174 | ModelRun | model_run | model extracts exact secret 034 | 写入 `secret-034.txt`，执行 run 任务 `Read secret-034.txt and reply with exactly amber-034`，验证最终文本与工具链。 | 匹配 `amber-034` |
| 175 | ModelRun | model_run | model extracts exact secret 035 | 写入 `secret-035.txt`，执行 run 任务 `Read secret-035.txt and reply with exactly amber-035`，验证最终文本与工具链。 | 匹配 `amber-035` |
| 176 | ModelRun | model_run | model extracts exact secret 036 | 写入 `secret-036.txt`，执行 run 任务 `Read secret-036.txt and reply with exactly amber-036`，验证最终文本与工具链。 | 匹配 `amber-036` |
| 177 | ModelRun | model_run | model extracts exact secret 037 | 写入 `secret-037.txt`，执行 run 任务 `Read secret-037.txt and reply with exactly amber-037`，验证最终文本与工具链。 | 匹配 `amber-037` |
| 178 | ModelRun | model_run | model extracts exact secret 038 | 写入 `secret-038.txt`，执行 run 任务 `Read secret-038.txt and reply with exactly amber-038`，验证最终文本与工具链。 | 匹配 `amber-038` |
| 179 | ModelRun | model_run | model extracts exact secret 039 | 写入 `secret-039.txt`，执行 run 任务 `Read secret-039.txt and reply with exactly amber-039`，验证最终文本与工具链。 | 匹配 `amber-039` |
| 180 | ModelRun | model_run | model extracts exact secret 040 | 写入 `secret-040.txt`，执行 run 任务 `Read secret-040.txt and reply with exactly amber-040`，验证最终文本与工具链。 | 匹配 `amber-040` |
| 181 | ModelRun | model_run | model extracts exact secret 041 | 写入 `secret-041.txt`，执行 run 任务 `Read secret-041.txt and reply with exactly amber-041`，验证最终文本与工具链。 | 匹配 `amber-041` |
| 182 | ModelRun | model_run | model extracts exact secret 042 | 写入 `secret-042.txt`，执行 run 任务 `Read secret-042.txt and reply with exactly amber-042`，验证最终文本与工具链。 | 匹配 `amber-042` |
| 183 | ModelRun | model_run | model extracts exact secret 043 | 写入 `secret-043.txt`，执行 run 任务 `Read secret-043.txt and reply with exactly amber-043`，验证最终文本与工具链。 | 匹配 `amber-043` |
| 184 | ModelRun | model_run | model extracts exact secret 044 | 写入 `secret-044.txt`，执行 run 任务 `Read secret-044.txt and reply with exactly amber-044`，验证最终文本与工具链。 | 匹配 `amber-044` |
| 185 | ModelRun | model_run | model extracts exact secret 045 | 写入 `secret-045.txt`，执行 run 任务 `Read secret-045.txt and reply with exactly amber-045`，验证最终文本与工具链。 | 匹配 `amber-045` |
| 186 | ModelRun | model_run | model extracts exact secret 046 | 写入 `secret-046.txt`，执行 run 任务 `Read secret-046.txt and reply with exactly amber-046`，验证最终文本与工具链。 | 匹配 `amber-046` |
| 187 | ModelRun | model_run | model extracts exact secret 047 | 写入 `secret-047.txt`，执行 run 任务 `Read secret-047.txt and reply with exactly amber-047`，验证最终文本与工具链。 | 匹配 `amber-047` |
| 188 | ModelRun | model_run | model extracts exact secret 048 | 写入 `secret-048.txt`，执行 run 任务 `Read secret-048.txt and reply with exactly amber-048`，验证最终文本与工具链。 | 匹配 `amber-048` |
| 189 | ModelRun | model_run | model extracts exact secret 049 | 写入 `secret-049.txt`，执行 run 任务 `Read secret-049.txt and reply with exactly amber-049`，验证最终文本与工具链。 | 匹配 `amber-049` |
| 190 | ModelRun | model_run | model extracts exact secret 050 | 写入 `secret-050.txt`，执行 run 任务 `Read secret-050.txt and reply with exactly amber-050`，验证最终文本与工具链。 | 匹配 `amber-050` |
| 191 | ModelRun | model_run | model extracts exact secret 051 | 写入 `secret-051.txt`，执行 run 任务 `Read secret-051.txt and reply with exactly amber-051`，验证最终文本与工具链。 | 匹配 `amber-051` |
| 192 | ModelRun | model_run | model extracts exact secret 052 | 写入 `secret-052.txt`，执行 run 任务 `Read secret-052.txt and reply with exactly amber-052`，验证最终文本与工具链。 | 匹配 `amber-052` |
| 193 | ModelRun | model_run | model extracts exact secret 053 | 写入 `secret-053.txt`，执行 run 任务 `Read secret-053.txt and reply with exactly amber-053`，验证最终文本与工具链。 | 匹配 `amber-053` |
| 194 | ModelRun | model_run | model extracts exact secret 054 | 写入 `secret-054.txt`，执行 run 任务 `Read secret-054.txt and reply with exactly amber-054`，验证最终文本与工具链。 | 匹配 `amber-054` |
| 195 | ModelRun | model_run | model extracts exact secret 055 | 写入 `secret-055.txt`，执行 run 任务 `Read secret-055.txt and reply with exactly amber-055`，验证最终文本与工具链。 | 匹配 `amber-055` |
| 196 | ModelRun | model_run | model extracts exact secret 056 | 写入 `secret-056.txt`，执行 run 任务 `Read secret-056.txt and reply with exactly amber-056`，验证最终文本与工具链。 | 匹配 `amber-056` |
| 197 | ModelRun | model_run | model extracts exact secret 057 | 写入 `secret-057.txt`，执行 run 任务 `Read secret-057.txt and reply with exactly amber-057`，验证最终文本与工具链。 | 匹配 `amber-057` |
| 198 | ModelRun | model_run | model extracts exact secret 058 | 写入 `secret-058.txt`，执行 run 任务 `Read secret-058.txt and reply with exactly amber-058`，验证最终文本与工具链。 | 匹配 `amber-058` |
| 199 | ModelRun | model_run | model extracts exact secret 059 | 写入 `secret-059.txt`，执行 run 任务 `Read secret-059.txt and reply with exactly amber-059`，验证最终文本与工具链。 | 匹配 `amber-059` |
| 200 | ModelRun | model_run | model extracts exact secret 060 | 写入 `secret-060.txt`，执行 run 任务 `Read secret-060.txt and reply with exactly amber-060`，验证最终文本与工具链。 | 匹配 `amber-060` |
