# OpenCode 用户级用例执行结果 001-200

- 执行时间(UTC): 2026-04-19T03:02:07.405963Z
- 运行目录: `/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206`
- 总计: 200，通过: 200，失败: 0
- 总耗时: 601.0s

## 模块通过率

- CLI: 20/20
- ChatCmd: 120/120
- ModelRun: 60/60

## 模型 token 统计（141-200）

- model_requests: 120
- approx_tokens_total: 105660
- read_file_calls: 60

| ID | 模块 | 用例标题 | 结果 | 关键说明 | 实际结果 |
| --- | --- | --- | --- | --- | --- |
| 001 | CLI | version shows build version | PASS | exit=0 expect=/.+/ | dev |
| 002 | CLI | help shows usage | PASS | exit=0 expect=/Usage:/ | tacli dev |
| 003 | CLI | status shows workspace summary | PASS | exit=0 expect=/workdir=/tmp/ | workdir=/tmp |
| 004 | CLI | plan missing file returns empty | PASS | exit=1 expect=/^$/.+/ | plan error: file does not exist |
| 005 | CLI | contract missing returns placeholder | PASS | exit=0 expect=/\(no task contract\)/\(missing\)/ | (no task contract) |
| 006 | CLI | capabilities lists packs | PASS | exit=0 expect=/ops:/ | ops: Inspect local operational state, logs, services, and health checks. |
| 007 | CLI | skills lists bundled skills | PASS | exit=0 expect=/bundled/ | Automotive BOM Normalizer [local]: --- |
| 008 | CLI | models command handles endpoint | PASS | exit=0 expect=/model API error/request failed/[A-Za-z0-9][A-Za-z0-9._/-]{2,}/ | BAAI/bge-reranker-v2-m3 |
| 009 | CLI | ping command handles endpoint | PASS | exit=0 expect=/pong/model API error/request failed/ | pong |
| 010 | CLI | unknown command exits with guidance | PASS | exit=0 expect=/missing task/Usage:/not sure what you want/If you meant a command/ | I’m not sure what you want done with `definitely-unknown-subcmd`. |
| 011 | CLI | run without task rejects | PASS | exit=2 expect=/missing task/Usage:/ | missing task |
| 012 | CLI | control status path | PASS | exit=0 expect=/state=/ | workdir=/root/tiny-agent-cli |
| 013 | CLI | control plan path | PASS | exit=0 expect=/.+/ | # tacli Roadmap |
| 014 | CLI | control contract path | PASS | exit=0 expect=/task contract/\(no task contract\)/\(missing\)/ | (no task contract) |
| 015 | CLI | capability detail query | PASS | exit=0 expect=/ops:/ | ops: Inspect local operational state, logs, services, and health checks. |
| 016 | CLI | skills against repo | PASS | exit=0 expect=/bundled/hooks/ | Automotive BOM Normalizer [local]: --- |
| 017 | CLI | status command_rules field | PASS | exit=0 expect=/command_rules=/ | workdir=/root/tiny-agent-cli |
| 018 | CLI | status sessions field | PASS | exit=0 expect=/sessions=/ | workdir=/root/tiny-agent-cli |
| 019 | CLI | version non-empty | PASS | exit=0 expect=/\S+/ | dev |
| 020 | CLI | help includes chat | PASS | exit=0 expect=/tacli chat/ | tacli dev |
| 021 | ChatCmd | slash help #1 | PASS | exit=0 expect=/tacli dev/ | tacli dev |
| 022 | ChatCmd | status view #2 | PASS | exit=0 expect=/conversation=/ | conversation=u002 |
| 023 | ChatCmd | new conversation #3 | PASS | exit=0 expect=/started conversation/ | started conversation demo |
| 024 | ChatCmd | resume missing conversation #4 | PASS | exit=0 expect=/no saved conversation/ | no saved conversation missing |
| 025 | ChatCmd | rename conversation #5 | PASS | exit=0 expect=/renamed conversation/ | renamed conversation u005 -> renamed |
| 026 | ChatCmd | fork conversation #6 | PASS | exit=0 expect=/forked conversation/ | forked conversation to branched |
| 027 | ChatCmd | tree rendering #7 | PASS | exit=0 expect=/conversation tree/current_conversation/ | current_conversation=u007 |
| 028 | ChatCmd | memory show #8 | PASS | exit=0 expect=/path=.*memory\.json/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/028.state/memory.json |
| 029 | ChatCmd | memory remember #9 | PASS | exit=0 expect=/project_notes=1/project memory saved/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/029.state/memory.json |
| 030 | ChatCmd | memory forget #10 | PASS | exit=0 expect=/removed/ | removed 0 project memory note(s) |
| 031 | ChatCmd | memory team show #11 | PASS | exit=0 expect=/team memory/team_notes=/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/031.state/memory.json |
| 032 | ChatCmd | tasks list #12 | PASS | exit=0 expect=/no tasks/task-/ | no tasks |
| 033 | ChatCmd | tasks create #13 | PASS | exit=0 expect=/task-001/ | task-001 [pending] sample-task |
| 034 | ChatCmd | tasks update unknown #14 | PASS | exit=0 expect=/unknown task/task-001/ | unknown task "task-001" |
| 035 | ChatCmd | tasks delete unknown #15 | PASS | exit=0 expect=/unknown task/deleted/ | unknown task "task-001" |
| 036 | ChatCmd | policy view #16 | PASS | exit=0 expect=/default=/ | default=prompt |
| 037 | ChatCmd | policy default allow #17 | PASS | exit=0 expect=/default=allow/ | default=allow |
| 038 | ChatCmd | policy default prompt #18 | PASS | exit=0 expect=/default=prompt/ | default=prompt |
| 039 | ChatCmd | policy command list #19 | PASS | exit=0 expect=/command/ | default=prompt |
| 040 | ChatCmd | audit stats #20 | PASS | exit=0 expect=/audit=/ | audit=0 |
| 041 | ChatCmd | debug-tool-call #21 | PASS | exit=0 expect=/audit/no audit events yet/ | no audit events yet |
| 042 | ChatCmd | debug-turn #22 | PASS | exit=0 expect=/turn summaries/no turn summaries yet/ | no turn summaries yet |
| 043 | ChatCmd | trace stats #23 | PASS | exit=0 expect=/events=/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/043.state/trace-u023.jsonl events=2 top=command:1,runtime_started:1 |
| 044 | ChatCmd | plugin list #24 | PASS | exit=0 expect=/plugin/ | no plugins discovered |
| 045 | ChatCmd | skills list #25 | PASS | exit=0 expect=/bundled/hooks/ | Automotive BOM Normalizer [local]: --- |
| 046 | ChatCmd | capabilities list #26 | PASS | exit=0 expect=/ops:/ | ops: Inspect local operational state, logs, services, and health checks. |
| 047 | ChatCmd | mcp list #27 | PASS | exit=0 expect=/MCP/no MCP servers configured/ | no MCP servers configured |
| 048 | ChatCmd | hooks list #28 | PASS | exit=0 expect=/PreToolUse/ | PreToolUse: |
| 049 | ChatCmd | agents list #29 | PASS | exit=0 expect=/background agents/no background agents/ | no background agents |
| 050 | ChatCmd | jobs list #30 | PASS | exit=0 expect=/background jobs/no background jobs/ | no background jobs |
| 051 | ChatCmd | job unknown #31 | PASS | exit=0 expect=/unknown job/ | unknown job "no-such" |
| 052 | ChatCmd | job-send unknown #32 | PASS | exit=0 expect=/unknown job/usage/ | unknown job "no-such" |
| 053 | ChatCmd | job-cancel unknown #33 | PASS | exit=0 expect=/unknown job/canceled/ | unknown job "no-such" |
| 054 | ChatCmd | job-apply unknown #34 | PASS | exit=0 expect=/unknown job/applied/ | unknown job "no-such" |
| 055 | ChatCmd | steer queue #35 | PASS | exit=0 expect=/queued steering message/ | queued steering message |
| 056 | ChatCmd | follow queue #36 | PASS | exit=0 expect=/queued follow-up message/ | queued follow-up message |
| 057 | ChatCmd | approval set allow #37 | PASS | exit=0 expect=/approval mode set to/ | approval mode set to allow |
| 058 | ChatCmd | approval set prompt #38 | PASS | exit=0 expect=/approval mode set to/ | approval mode set to prompt |
| 059 | ChatCmd | model set #39 | PASS | exit=0 expect=/model set to test-model/ | model set to test-model for this session |
| 060 | ChatCmd | reset context #40 | PASS | exit=0 expect=/context reset/ | context reset |
| 061 | ChatCmd | slash help #41 | PASS | exit=0 expect=/tacli dev/ | tacli dev |
| 062 | ChatCmd | status view #42 | PASS | exit=0 expect=/conversation=/ | conversation=u042 |
| 063 | ChatCmd | new conversation #43 | PASS | exit=0 expect=/started conversation/ | started conversation demo |
| 064 | ChatCmd | resume missing conversation #44 | PASS | exit=0 expect=/no saved conversation/ | no saved conversation missing |
| 065 | ChatCmd | rename conversation #45 | PASS | exit=0 expect=/renamed conversation/ | renamed conversation u045 -> renamed |
| 066 | ChatCmd | fork conversation #46 | PASS | exit=0 expect=/forked conversation/ | forked conversation to branched |
| 067 | ChatCmd | tree rendering #47 | PASS | exit=0 expect=/conversation tree/current_conversation/ | current_conversation=u047 |
| 068 | ChatCmd | memory show #48 | PASS | exit=0 expect=/path=.*memory\.json/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/068.state/memory.json |
| 069 | ChatCmd | memory remember #49 | PASS | exit=0 expect=/project_notes=1/project memory saved/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/069.state/memory.json |
| 070 | ChatCmd | memory forget #50 | PASS | exit=0 expect=/removed/ | removed 0 project memory note(s) |
| 071 | ChatCmd | memory team show #51 | PASS | exit=0 expect=/team memory/team_notes=/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/071.state/memory.json |
| 072 | ChatCmd | tasks list #52 | PASS | exit=0 expect=/no tasks/task-/ | no tasks |
| 073 | ChatCmd | tasks create #53 | PASS | exit=0 expect=/task-001/ | task-001 [pending] sample-task |
| 074 | ChatCmd | tasks update unknown #54 | PASS | exit=0 expect=/unknown task/task-001/ | unknown task "task-001" |
| 075 | ChatCmd | tasks delete unknown #55 | PASS | exit=0 expect=/unknown task/deleted/ | unknown task "task-001" |
| 076 | ChatCmd | policy view #56 | PASS | exit=0 expect=/default=/ | default=prompt |
| 077 | ChatCmd | policy default allow #57 | PASS | exit=0 expect=/default=allow/ | default=allow |
| 078 | ChatCmd | policy default prompt #58 | PASS | exit=0 expect=/default=prompt/ | default=prompt |
| 079 | ChatCmd | policy command list #59 | PASS | exit=0 expect=/command/ | default=prompt |
| 080 | ChatCmd | audit stats #60 | PASS | exit=0 expect=/audit=/ | audit=0 |
| 081 | ChatCmd | debug-tool-call #61 | PASS | exit=0 expect=/audit/no audit events yet/ | no audit events yet |
| 082 | ChatCmd | debug-turn #62 | PASS | exit=0 expect=/turn summaries/no turn summaries yet/ | no turn summaries yet |
| 083 | ChatCmd | trace stats #63 | PASS | exit=0 expect=/events=/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/083.state/trace-u063.jsonl events=2 top=command:1,runtime_started:1 |
| 084 | ChatCmd | plugin list #64 | PASS | exit=0 expect=/plugin/ | no plugins discovered |
| 085 | ChatCmd | skills list #65 | PASS | exit=0 expect=/bundled/hooks/ | Automotive BOM Normalizer [local]: --- |
| 086 | ChatCmd | capabilities list #66 | PASS | exit=0 expect=/ops:/ | ops: Inspect local operational state, logs, services, and health checks. |
| 087 | ChatCmd | mcp list #67 | PASS | exit=0 expect=/MCP/no MCP servers configured/ | no MCP servers configured |
| 088 | ChatCmd | hooks list #68 | PASS | exit=0 expect=/PreToolUse/ | PreToolUse: |
| 089 | ChatCmd | agents list #69 | PASS | exit=0 expect=/background agents/no background agents/ | no background agents |
| 090 | ChatCmd | jobs list #70 | PASS | exit=0 expect=/background jobs/no background jobs/ | no background jobs |
| 091 | ChatCmd | job unknown #71 | PASS | exit=0 expect=/unknown job/ | unknown job "no-such" |
| 092 | ChatCmd | job-send unknown #72 | PASS | exit=0 expect=/unknown job/usage/ | unknown job "no-such" |
| 093 | ChatCmd | job-cancel unknown #73 | PASS | exit=0 expect=/unknown job/canceled/ | unknown job "no-such" |
| 094 | ChatCmd | job-apply unknown #74 | PASS | exit=0 expect=/unknown job/applied/ | unknown job "no-such" |
| 095 | ChatCmd | steer queue #75 | PASS | exit=0 expect=/queued steering message/ | queued steering message |
| 096 | ChatCmd | follow queue #76 | PASS | exit=0 expect=/queued follow-up message/ | queued follow-up message |
| 097 | ChatCmd | approval set allow #77 | PASS | exit=0 expect=/approval mode set to/ | approval mode set to allow |
| 098 | ChatCmd | approval set prompt #78 | PASS | exit=0 expect=/approval mode set to/ | approval mode set to prompt |
| 099 | ChatCmd | model set #79 | PASS | exit=0 expect=/model set to test-model/ | model set to test-model for this session |
| 100 | ChatCmd | reset context #80 | PASS | exit=0 expect=/context reset/ | context reset |
| 101 | ChatCmd | slash help #81 | PASS | exit=0 expect=/tacli dev/ | tacli dev |
| 102 | ChatCmd | status view #82 | PASS | exit=0 expect=/conversation=/ | conversation=u082 |
| 103 | ChatCmd | new conversation #83 | PASS | exit=0 expect=/started conversation/ | started conversation demo |
| 104 | ChatCmd | resume missing conversation #84 | PASS | exit=0 expect=/no saved conversation/ | no saved conversation missing |
| 105 | ChatCmd | rename conversation #85 | PASS | exit=0 expect=/renamed conversation/ | renamed conversation u085 -> renamed |
| 106 | ChatCmd | fork conversation #86 | PASS | exit=0 expect=/forked conversation/ | forked conversation to branched |
| 107 | ChatCmd | tree rendering #87 | PASS | exit=0 expect=/conversation tree/current_conversation/ | current_conversation=u087 |
| 108 | ChatCmd | memory show #88 | PASS | exit=0 expect=/path=.*memory\.json/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/108.state/memory.json |
| 109 | ChatCmd | memory remember #89 | PASS | exit=0 expect=/project_notes=1/project memory saved/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/109.state/memory.json |
| 110 | ChatCmd | memory forget #90 | PASS | exit=0 expect=/removed/ | removed 0 project memory note(s) |
| 111 | ChatCmd | memory team show #91 | PASS | exit=0 expect=/team memory/team_notes=/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/111.state/memory.json |
| 112 | ChatCmd | tasks list #92 | PASS | exit=0 expect=/no tasks/task-/ | no tasks |
| 113 | ChatCmd | tasks create #93 | PASS | exit=0 expect=/task-001/ | task-001 [pending] sample-task |
| 114 | ChatCmd | tasks update unknown #94 | PASS | exit=0 expect=/unknown task/task-001/ | unknown task "task-001" |
| 115 | ChatCmd | tasks delete unknown #95 | PASS | exit=0 expect=/unknown task/deleted/ | unknown task "task-001" |
| 116 | ChatCmd | policy view #96 | PASS | exit=0 expect=/default=/ | default=prompt |
| 117 | ChatCmd | policy default allow #97 | PASS | exit=0 expect=/default=allow/ | default=allow |
| 118 | ChatCmd | policy default prompt #98 | PASS | exit=0 expect=/default=prompt/ | default=prompt |
| 119 | ChatCmd | policy command list #99 | PASS | exit=0 expect=/command/ | default=prompt |
| 120 | ChatCmd | audit stats #100 | PASS | exit=0 expect=/audit=/ | audit=0 |
| 121 | ChatCmd | debug-tool-call #101 | PASS | exit=0 expect=/audit/no audit events yet/ | no audit events yet |
| 122 | ChatCmd | debug-turn #102 | PASS | exit=0 expect=/turn summaries/no turn summaries yet/ | no turn summaries yet |
| 123 | ChatCmd | trace stats #103 | PASS | exit=0 expect=/events=/ | path=/root/tiny-agent-cli/.tacli/opencode-user-e2e/runs/20260419-025206/123.state/trace-u103.jsonl events=2 top=command:1,runtime_started:1 |
| 124 | ChatCmd | plugin list #104 | PASS | exit=0 expect=/plugin/ | no plugins discovered |
| 125 | ChatCmd | skills list #105 | PASS | exit=0 expect=/bundled/hooks/ | Automotive BOM Normalizer [local]: --- |
| 126 | ChatCmd | capabilities list #106 | PASS | exit=0 expect=/ops:/ | ops: Inspect local operational state, logs, services, and health checks. |
| 127 | ChatCmd | mcp list #107 | PASS | exit=0 expect=/MCP/no MCP servers configured/ | no MCP servers configured |
| 128 | ChatCmd | hooks list #108 | PASS | exit=0 expect=/PreToolUse/ | PreToolUse: |
| 129 | ChatCmd | agents list #109 | PASS | exit=0 expect=/background agents/no background agents/ | no background agents |
| 130 | ChatCmd | jobs list #110 | PASS | exit=0 expect=/background jobs/no background jobs/ | no background jobs |
| 131 | ChatCmd | job unknown #111 | PASS | exit=0 expect=/unknown job/ | unknown job "no-such" |
| 132 | ChatCmd | job-send unknown #112 | PASS | exit=0 expect=/unknown job/usage/ | unknown job "no-such" |
| 133 | ChatCmd | job-cancel unknown #113 | PASS | exit=0 expect=/unknown job/canceled/ | unknown job "no-such" |
| 134 | ChatCmd | job-apply unknown #114 | PASS | exit=0 expect=/unknown job/applied/ | unknown job "no-such" |
| 135 | ChatCmd | steer queue #115 | PASS | exit=0 expect=/queued steering message/ | queued steering message |
| 136 | ChatCmd | follow queue #116 | PASS | exit=0 expect=/queued follow-up message/ | queued follow-up message |
| 137 | ChatCmd | approval set allow #117 | PASS | exit=0 expect=/approval mode set to/ | approval mode set to allow |
| 138 | ChatCmd | approval set prompt #118 | PASS | exit=0 expect=/approval mode set to/ | approval mode set to prompt |
| 139 | ChatCmd | model set #119 | PASS | exit=0 expect=/model set to test-model/ | model set to test-model for this session |
| 140 | ChatCmd | reset context #120 | PASS | exit=0 expect=/context reset/ | context reset |
| 141 | ModelRun | model extracts exact secret 001 | PASS | exit=0 expect=amber-001 used_read=True | amber-001 |
| 142 | ModelRun | model extracts exact secret 002 | PASS | exit=0 expect=amber-002 used_read=True | amber-002 |
| 143 | ModelRun | model extracts exact secret 003 | PASS | exit=0 expect=amber-003 used_read=True | amber-003 |
| 144 | ModelRun | model extracts exact secret 004 | PASS | exit=0 expect=amber-004 used_read=True | amber-004 |
| 145 | ModelRun | model extracts exact secret 005 | PASS | exit=0 expect=amber-005 used_read=True | amber-005 |
| 146 | ModelRun | model extracts exact secret 006 | PASS | exit=0 expect=amber-006 used_read=True | amber-006 |
| 147 | ModelRun | model extracts exact secret 007 | PASS | exit=0 expect=amber-007 used_read=True | amber-007 |
| 148 | ModelRun | model extracts exact secret 008 | PASS | exit=0 expect=amber-008 used_read=True | amber-008 |
| 149 | ModelRun | model extracts exact secret 009 | PASS | exit=0 expect=amber-009 used_read=True | amber-009 |
| 150 | ModelRun | model extracts exact secret 010 | PASS | exit=0 expect=amber-010 used_read=True | amber-010 |
| 151 | ModelRun | model extracts exact secret 011 | PASS | exit=0 expect=amber-011 used_read=True | amber-011 |
| 152 | ModelRun | model extracts exact secret 012 | PASS | exit=0 expect=amber-012 used_read=True | amber-012 |
| 153 | ModelRun | model extracts exact secret 013 | PASS | exit=0 expect=amber-013 used_read=True | amber-013 |
| 154 | ModelRun | model extracts exact secret 014 | PASS | exit=0 expect=amber-014 used_read=True | amber-014 |
| 155 | ModelRun | model extracts exact secret 015 | PASS | exit=0 expect=amber-015 used_read=True | amber-015 |
| 156 | ModelRun | model extracts exact secret 016 | PASS | exit=0 expect=amber-016 used_read=True | amber-016 |
| 157 | ModelRun | model extracts exact secret 017 | PASS | exit=0 expect=amber-017 used_read=True | amber-017 |
| 158 | ModelRun | model extracts exact secret 018 | PASS | exit=0 expect=amber-018 used_read=True | amber-018 |
| 159 | ModelRun | model extracts exact secret 019 | PASS | exit=0 expect=amber-019 used_read=True | amber-019 |
| 160 | ModelRun | model extracts exact secret 020 | PASS | exit=0 expect=amber-020 used_read=True | amber-020 |
| 161 | ModelRun | model extracts exact secret 021 | PASS | exit=0 expect=amber-021 used_read=True | amber-021 |
| 162 | ModelRun | model extracts exact secret 022 | PASS | exit=0 expect=amber-022 used_read=True | amber-022 |
| 163 | ModelRun | model extracts exact secret 023 | PASS | exit=0 expect=amber-023 used_read=True | amber-023 |
| 164 | ModelRun | model extracts exact secret 024 | PASS | exit=0 expect=amber-024 used_read=True | amber-024 |
| 165 | ModelRun | model extracts exact secret 025 | PASS | exit=0 expect=amber-025 used_read=True | amber-025 |
| 166 | ModelRun | model extracts exact secret 026 | PASS | exit=0 expect=amber-026 used_read=True | amber-026 |
| 167 | ModelRun | model extracts exact secret 027 | PASS | exit=0 expect=amber-027 used_read=True | amber-027 |
| 168 | ModelRun | model extracts exact secret 028 | PASS | exit=0 expect=amber-028 used_read=True | amber-028 |
| 169 | ModelRun | model extracts exact secret 029 | PASS | exit=0 expect=amber-029 used_read=True | amber-029 |
| 170 | ModelRun | model extracts exact secret 030 | PASS | exit=0 expect=amber-030 used_read=True | amber-030 |
| 171 | ModelRun | model extracts exact secret 031 | PASS | exit=0 expect=amber-031 used_read=True | amber-031 |
| 172 | ModelRun | model extracts exact secret 032 | PASS | exit=0 expect=amber-032 used_read=True | amber-032 |
| 173 | ModelRun | model extracts exact secret 033 | PASS | exit=0 expect=amber-033 used_read=True | amber-033 |
| 174 | ModelRun | model extracts exact secret 034 | PASS | exit=0 expect=amber-034 used_read=True | amber-034 |
| 175 | ModelRun | model extracts exact secret 035 | PASS | exit=0 expect=amber-035 used_read=True | amber-035 |
| 176 | ModelRun | model extracts exact secret 036 | PASS | exit=0 expect=amber-036 used_read=True | amber-036 |
| 177 | ModelRun | model extracts exact secret 037 | PASS | exit=0 expect=amber-037 used_read=True | amber-037 |
| 178 | ModelRun | model extracts exact secret 038 | PASS | exit=0 expect=amber-038 used_read=True | amber-038 |
| 179 | ModelRun | model extracts exact secret 039 | PASS | exit=0 expect=amber-039 used_read=True | amber-039 |
| 180 | ModelRun | model extracts exact secret 040 | PASS | exit=0 expect=amber-040 used_read=True | amber-040 |
| 181 | ModelRun | model extracts exact secret 041 | PASS | exit=0 expect=amber-041 used_read=True | amber-041 |
| 182 | ModelRun | model extracts exact secret 042 | PASS | exit=0 expect=amber-042 used_read=True | amber-042 |
| 183 | ModelRun | model extracts exact secret 043 | PASS | exit=0 expect=amber-043 used_read=True | amber-043 |
| 184 | ModelRun | model extracts exact secret 044 | PASS | exit=0 expect=amber-044 used_read=True | amber-044 |
| 185 | ModelRun | model extracts exact secret 045 | PASS | exit=0 expect=amber-045 used_read=True | amber-045 |
| 186 | ModelRun | model extracts exact secret 046 | PASS | exit=0 expect=amber-046 used_read=True | amber-046 |
| 187 | ModelRun | model extracts exact secret 047 | PASS | exit=0 expect=amber-047 used_read=True | amber-047 |
| 188 | ModelRun | model extracts exact secret 048 | PASS | exit=0 expect=amber-048 used_read=True | amber-048 |
| 189 | ModelRun | model extracts exact secret 049 | PASS | exit=0 expect=amber-049 used_read=True | amber-049 |
| 190 | ModelRun | model extracts exact secret 050 | PASS | exit=0 expect=amber-050 used_read=True | amber-050 |
| 191 | ModelRun | model extracts exact secret 051 | PASS | exit=0 expect=amber-051 used_read=True | amber-051 |
| 192 | ModelRun | model extracts exact secret 052 | PASS | exit=0 expect=amber-052 used_read=True | amber-052 |
| 193 | ModelRun | model extracts exact secret 053 | PASS | exit=0 expect=amber-053 used_read=True | amber-053 |
| 194 | ModelRun | model extracts exact secret 054 | PASS | exit=0 expect=amber-054 used_read=True | amber-054 |
| 195 | ModelRun | model extracts exact secret 055 | PASS | exit=0 expect=amber-055 used_read=True | amber-055 |
| 196 | ModelRun | model extracts exact secret 056 | PASS | exit=0 expect=amber-056 used_read=True | amber-056 |
| 197 | ModelRun | model extracts exact secret 057 | PASS | exit=0 expect=amber-057 used_read=True | amber-057 |
| 198 | ModelRun | model extracts exact secret 058 | PASS | exit=0 expect=amber-058 used_read=True | amber-058 |
| 199 | ModelRun | model extracts exact secret 059 | PASS | exit=0 expect=amber-059 used_read=True | amber-059 |
| 200 | ModelRun | model extracts exact secret 060 | PASS | exit=0 expect=amber-060 used_read=True | amber-060 |
