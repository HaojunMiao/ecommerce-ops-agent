-- 删除已被现行实现取代且无运行时读写的遗留表。
-- 权限使用 users.role / workspace_members.role；异步任务使用 Redis + Asynq；
-- 工具调用存入 messages.tool_calls 并由 tool_invocations 审计；Skill 由 AgentVersion 直接绑定版本。
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;

DROP TABLE IF EXISTS dead_letters;
DROP TABLE IF EXISTS job_schedules;
DROP TABLE IF EXISTS jobs;

DROP TABLE IF EXISTS tool_calls;
DROP TABLE IF EXISTS skill_subscriptions;
