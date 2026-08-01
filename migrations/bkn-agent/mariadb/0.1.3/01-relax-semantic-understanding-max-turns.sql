-- Copyright 2026 openbkn.ai
--
-- Licensed under the Apache License, Version 2.0.
-- See the LICENSE file in the project root for details.

-- 语义理解内置 agent 的 max_turns 从 1 放宽到 5（#447 后续加固）。
--
-- 0.1.2 seed 写的是 max_turns=1 → recursion_limit = 1*2+1 = 3。工具面改成零默认
-- 之后，图只剩单个 model 节点，1 轮确实够用；但 1 把「恰好一次模型调用、零工具
-- 轮次」写死进了数据：以后给这两个 agent 挂上 toolbox、或 langgraph 多长出一个
-- 节点，都会以同一句 GraphRecursionError 再挂一次。零工具时放大预算不产生任何
-- 额外调用（没有 tools 节点就无从循环），真正的兜底仍是 timeout_s=300。
--
-- 只改仍停留在 seed 原值（1）的行：运维已按自己需要调过的值不覆盖。
-- 重复执行安全——第二次跑时 where 条件不再命中。
USE openbkn;

update t_agent
set f_limits = json_set(f_limits, '$.max_turns', 5),
    f_update_time = unix_timestamp(now(3)) * 1000
where f_agent_id in (
    'resource-semantic-understanding',
    'catalog-semantic-understanding'
)
and json_extract(f_limits, '$.max_turns') = 1;
