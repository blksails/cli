-- ssh-key-provisioning：cli.ssh_keys 建表 + 管理员判定 + RLS 迁移
--
-- 数据放在 bk CLI 专属的独立 schema `cli`（与应用域 `blacksail` 隔离），
-- 便于 CLI 工具数据单独管理、授权与审计。
--
-- 安全模型为 bootstrap 管理员代装：普通用户在本机生成 ed25519 私钥（0600，永不离机），
-- 仅把公钥登记到 cli.ssh_keys（初始 pending）；管理员经 RLS 读全部 pending、
-- 代装到 Dokku 后回写 installed/revoked。本表只存公钥/指纹/状态，绝不存私钥。
--
-- 全部 DDL 与策略幂等（schema/table IF NOT EXISTS、函数 CREATE OR REPLACE、
-- 策略 DROP POLICY IF EXISTS 后重建、触发器 DROP TRIGGER IF EXISTS 后重建），
-- 整个脚本可重复执行无错。
--
-- ⚠️ PostgREST 暴露：要让 bk 经 PostgREST 访问 `cli` schema，须在 Supabase 项目
--    的 API 设置（Exposed schemas / db.schemas / PGRST_DB_SCHEMAS）中加入 `cli`，
--    否则 REST 请求会 404/PGRST106。本脚本已对 authenticated/service_role 授予
--    schema usage 与表权限；暴露 schema 属项目配置，迁移无法代劳。
--
-- 对应 requirements 7.1–7.3、8.1–8.5；design「Data Models」「RLS」段。

-- ---------------------------------------------------------------------------
-- 独立 schema：cli（CLI 工具专属，与 blacksail 应用域隔离）
-- ---------------------------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS cli;

-- ---------------------------------------------------------------------------
-- 管理员名单 + 判定函数
--   admins：管理员 user_id（= auth.uid()）名单。
--   is_admin()：SECURITY DEFINER，在 RLS 策略内以提权身份判断当前会话是否管理员，
--   避免普通用户因无法 SELECT admins 表而判定失败；并固定 search_path 防注入。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cli.admins (
  user_id uuid PRIMARY KEY
);

CREATE OR REPLACE FUNCTION cli.is_admin()
RETURNS boolean
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = cli, pg_temp
AS $$
  SELECT EXISTS (
    SELECT 1 FROM cli.admins WHERE user_id = auth.uid()
  );
$$;

-- ---------------------------------------------------------------------------
-- ssh_keys：公钥登记 + 状态机（pending/installed/revoked）+ 审计字段
--   unique(owner, host)：一用户一主机一把活跃密钥；re-provision 经 upsert 覆盖。
--   不含任何私钥字段（仅 public_key 授权行 + fingerprint）。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cli.ssh_keys (
  id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  owner        uuid        NOT NULL DEFAULT auth.uid(),
  name         text        NOT NULL,                          -- 派生 bk-<emaillocal>-<host>，作 dokku ssh-keys 名
  host         text        NOT NULL,
  dokku_user   text        NOT NULL DEFAULT 'dokku',
  public_key   text        NOT NULL,                          -- authorized line（公钥，绝不存私钥）
  fingerprint  text        NOT NULL,                          -- SHA256:...
  status       text        NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending', 'installed', 'revoked')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  installed_by uuid,
  installed_at timestamptz,
  revoked_by   uuid,
  revoked_at   timestamptz,
  UNIQUE (owner, host)
);

-- ---------------------------------------------------------------------------
-- 行级安全（RLS）
--   多条 permissive 策略取并集：
--     - owner 仅能增改查询本人记录，且只能把行写成 pending（防自我提权为 installed）。
--     - 管理员经 is_admin() 读全部、改状态（installed/revoked）。
-- ---------------------------------------------------------------------------
ALTER TABLE cli.ssh_keys ENABLE ROW LEVEL SECURITY;

-- INSERT：仅能为自己登记，且初始状态必须为 pending。
DROP POLICY IF EXISTS ssh_keys_insert_owner ON cli.ssh_keys;
CREATE POLICY ssh_keys_insert_owner
  ON cli.ssh_keys
  FOR INSERT
  WITH CHECK (owner = auth.uid() AND status = 'pending');

-- SELECT：owner 看自己的；管理员看全部。
DROP POLICY IF EXISTS ssh_keys_select_owner_or_admin ON cli.ssh_keys;
CREATE POLICY ssh_keys_select_owner_or_admin
  ON cli.ssh_keys
  FOR SELECT
  USING (owner = auth.uid() OR cli.is_admin());

-- UPDATE（owner 自助）：仅能改自己的行，且改后状态仍须为 pending。
--   这条 WITH CHECK status = 'pending' 即「自我提权防护」——普通用户不能把自己的行改成 installed。
DROP POLICY IF EXISTS ssh_keys_update_owner ON cli.ssh_keys;
CREATE POLICY ssh_keys_update_owner
  ON cli.ssh_keys
  FOR UPDATE
  USING (owner = auth.uid())
  WITH CHECK (owner = auth.uid() AND status = 'pending');

-- UPDATE（管理员改状态）：管理员可把任意行写成 installed/revoked。
DROP POLICY IF EXISTS ssh_keys_update_admin ON cli.ssh_keys;
CREATE POLICY ssh_keys_update_admin
  ON cli.ssh_keys
  FOR UPDATE
  USING (cli.is_admin())
  WITH CHECK (cli.is_admin());

-- ---------------------------------------------------------------------------
-- 审计一致性触发器（design 末提供的可选 BEFORE UPDATE）
--   强制 installed_by / revoked_by 在被设值时等于 auth.uid()，
--   使审计「操作者」字段无法被伪造为他人身份。幂等：CREATE OR REPLACE + DROP TRIGGER IF EXISTS。
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION cli.ssh_keys_enforce_actor()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.installed_by IS NOT NULL
     AND NEW.installed_by IS DISTINCT FROM OLD.installed_by
     AND NEW.installed_by <> auth.uid() THEN
    RAISE EXCEPTION 'installed_by must equal auth.uid()';
  END IF;
  IF NEW.revoked_by IS NOT NULL
     AND NEW.revoked_by IS DISTINCT FROM OLD.revoked_by
     AND NEW.revoked_by <> auth.uid() THEN
    RAISE EXCEPTION 'revoked_by must equal auth.uid()';
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS ssh_keys_enforce_actor ON cli.ssh_keys;
CREATE TRIGGER ssh_keys_enforce_actor
  BEFORE UPDATE ON cli.ssh_keys
  FOR EACH ROW
  EXECUTE FUNCTION cli.ssh_keys_enforce_actor();

-- ---------------------------------------------------------------------------
-- 角色授权（Supabase 的 authenticated / service_role）
--   独立 schema 不会自动授权，须显式 GRANT，PostgREST 才能在 RLS 之上访问。
--   用 role-existence 守卫，使脚本在没有这些角色的 vanilla Postgres（RLS 验证脚本）
--   下也能幂等执行而不报错。RLS 仍是真正的行级访问控制；GRANT 只是表级前置许可。
-- ---------------------------------------------------------------------------
DO $$
DECLARE
  r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['authenticated', 'service_role'] LOOP
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('GRANT USAGE ON SCHEMA cli TO %I', r);
      EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON cli.ssh_keys TO %I', r);
      EXECUTE format('GRANT EXECUTE ON FUNCTION cli.is_admin() TO %I', r);
    END IF;
  END LOOP;
END
$$;
