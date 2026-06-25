-- proxy-target-sync：cli.proxy_targets 集中管理 yproxy hub 的 forward_targets allowlist
--
-- 把「哪些目标可经 hub 转发」从 hub 主机上手改 config.yaml，收敛为 Supabase 里
-- 可审计、带 RLS 权限的中心配置：普通用户可查看放行清单，仅管理员可增删；
-- 由 `bk proxy target sync` 渲染进 hub config.yaml 并重启 hub 生效（单向：Supabase 为真源）。
--
-- 复用 cli schema 与 cli.is_admin()（ssh_keys.sql 已建）。全部 DDL/策略幂等，可重复执行。
--
-- ⚠️ PostgREST 暴露：bk 经 PostgREST 访问 `cli` schema 须在 PGRST_DB_SCHEMAS / authenticator
--    的 pgrst.db_schemas 中包含 `cli`（ssh_keys 已要求，沿用即可）。

-- 独立 schema：cli（CLI 工具专属，幂等）
CREATE SCHEMA IF NOT EXISTS cli;

-- ---------------------------------------------------------------------------
-- proxy_targets：每行一个允许的转发目标（按 app_id 归组）
--   target 为 allowlist 模式：精确 host:port、host:*、*:port、*（与 yamuxproxy 一致）。
--   unique(app_id,target) 防重复；不存私钥/密钥类敏感信息。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cli.proxy_targets (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  app_id      text        NOT NULL,                 -- yproxy app id（如 infra）
  target      text        NOT NULL,                 -- host:port / host:* / *:port / *
  note        text,                                 -- 备注（可空）
  created_by  uuid        NOT NULL DEFAULT auth.uid(),
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (app_id, target)
);

-- ---------------------------------------------------------------------------
-- 行级安全（RLS）
--   SELECT：任意已认证用户可读（查看允许清单，便于自助发现可转发目标）。
--   INSERT/UPDATE/DELETE：仅管理员（cli.is_admin()），与 allowlist 作为安全控制相称。
-- ---------------------------------------------------------------------------
ALTER TABLE cli.proxy_targets ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS proxy_targets_select_authenticated ON cli.proxy_targets;
CREATE POLICY proxy_targets_select_authenticated
  ON cli.proxy_targets
  FOR SELECT
  USING (auth.uid() IS NOT NULL);

DROP POLICY IF EXISTS proxy_targets_insert_admin ON cli.proxy_targets;
CREATE POLICY proxy_targets_insert_admin
  ON cli.proxy_targets
  FOR INSERT
  WITH CHECK (cli.is_admin());

DROP POLICY IF EXISTS proxy_targets_update_admin ON cli.proxy_targets;
CREATE POLICY proxy_targets_update_admin
  ON cli.proxy_targets
  FOR UPDATE
  USING (cli.is_admin())
  WITH CHECK (cli.is_admin());

DROP POLICY IF EXISTS proxy_targets_delete_admin ON cli.proxy_targets;
CREATE POLICY proxy_targets_delete_admin
  ON cli.proxy_targets
  FOR DELETE
  USING (cli.is_admin());

-- ---------------------------------------------------------------------------
-- 角色授权（Supabase 的 authenticated / service_role），role-existence 守卫保持幂等，
-- 使脚本在无这些角色的 vanilla Postgres（RLS 验证）下也能执行。USAGE ON SCHEMA cli
-- 已由 ssh_keys.sql 授予，此处只补表权限。
-- ---------------------------------------------------------------------------
DO $$
DECLARE
  r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['authenticated', 'service_role'] LOOP
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('GRANT USAGE ON SCHEMA cli TO %I', r);
      EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON cli.proxy_targets TO %I', r);
    END IF;
  END LOOP;
END
$$;
