-- apps-host-directory：cli.hosts 建表 + RLS 迁移
--
-- 在线维护一份「Dokku 主机目录」，让 bk 客户端登录后自动拉取 SSH 连接配置
-- （host / user / port），免去每台机器手工配置 .bs.yaml 的 ssh 块。
--
-- 数据放在 bk CLI 专属 schema `cli`（与应用域 blacksail 隔离），与 cli.ssh_keys 同schema，
-- 复用其 cli.is_admin() 判定函数（本脚本假定 ssh_keys.sql 已先行执行；为可独立运行也
-- 幂等地 CREATE OR REPLACE 了 is_admin 与 admins 表的兜底定义）。
--
-- 安全模型：
--   - 任意已登录用户（authenticated）可 SELECT 主机目录（只读连接元数据，不含任何私钥/密码）。
--   - 仅管理员（cli.is_admin()）可 INSERT/UPDATE/DELETE 维护目录。
--   - 本表绝不存储私钥、密码或 identity 路径——私钥与本机安全选项（identity/insecure）
--     始终由客户端本地 .bs.yaml 提供，服务端只下发可公开的连接坐标。
--
-- 全部 DDL 与策略幂等，整个脚本可重复执行无错。
--
-- ⚠️ PostgREST 暴露：要让 bk 经 PostgREST 访问 `cli` schema，须在 Supabase 项目 API 设置
--    （Exposed schemas / db.schemas / PGRST_DB_SCHEMAS）中包含 `cli`，否则 REST 请求 404/PGRST106。

-- ---------------------------------------------------------------------------
-- schema 与管理员判定（与 ssh_keys.sql 一致；此处幂等兜底，便于独立执行）
-- ---------------------------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS cli;

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
-- hosts：Dokku 主机目录
--   name        命名标识（唯一），如 prod / staging / ad6，供客户端按名选择。
--   host        主机地址（IP 或域名）。
--   ssh_user    SSH 登录用户；NULL 时由客户端下游 dokku.New 默认为 'dokku'。
--   ssh_port    SSH 端口，默认 22。
--   is_default  是否为默认主机：未指定名称时客户端选用 is_default=true 的那条。
--   description 备注（可选）。
--   不含任何私钥/密码/identity 字段（仅可公开的连接坐标）。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS cli.hosts (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text        NOT NULL UNIQUE,
  host        text        NOT NULL,
  ssh_user    text,
  ssh_port    integer     NOT NULL DEFAULT 22 CHECK (ssh_port > 0 AND ssh_port <= 65535),
  is_default  boolean     NOT NULL DEFAULT false,
  description text,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- 至多一条默认主机：对 is_default=true 建唯一部分索引。
CREATE UNIQUE INDEX IF NOT EXISTS hosts_single_default
  ON cli.hosts ((is_default)) WHERE is_default;

-- updated_at 自动维护。
CREATE OR REPLACE FUNCTION cli.touch_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS hosts_touch_updated_at ON cli.hosts;
CREATE TRIGGER hosts_touch_updated_at
  BEFORE UPDATE ON cli.hosts
  FOR EACH ROW EXECUTE FUNCTION cli.touch_updated_at();

-- ---------------------------------------------------------------------------
-- 权限与 RLS
-- ---------------------------------------------------------------------------
GRANT USAGE ON SCHEMA cli TO authenticated, service_role;
GRANT SELECT ON cli.hosts TO authenticated;
GRANT ALL ON cli.hosts TO service_role;

ALTER TABLE cli.hosts ENABLE ROW LEVEL SECURITY;

-- 读：任意已登录用户可读主机目录。
DROP POLICY IF EXISTS hosts_select_authenticated ON cli.hosts;
CREATE POLICY hosts_select_authenticated ON cli.hosts
  FOR SELECT TO authenticated
  USING (true);

-- 写：仅管理员可维护目录。
DROP POLICY IF EXISTS hosts_insert_admin ON cli.hosts;
CREATE POLICY hosts_insert_admin ON cli.hosts
  FOR INSERT TO authenticated
  WITH CHECK (cli.is_admin());

DROP POLICY IF EXISTS hosts_update_admin ON cli.hosts;
CREATE POLICY hosts_update_admin ON cli.hosts
  FOR UPDATE TO authenticated
  USING (cli.is_admin())
  WITH CHECK (cli.is_admin());

DROP POLICY IF EXISTS hosts_delete_admin ON cli.hosts;
CREATE POLICY hosts_delete_admin ON cli.hosts
  FOR DELETE TO authenticated
  USING (cli.is_admin());
