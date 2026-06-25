-- proxy-hub 目录：cli.proxy_hub 让客户端「登录即用」proxy 隧道
--
-- 与 cli.hosts（SSH 主机目录）同构：登录后 bk 自动拉取本表缓存到本地，resolveHubConfig
-- 在本地 .bs.yaml 未配 proxy.* 时回退到该目录，从而 `bk proxy forward` 零配置可用。
--
-- 字段：server(host:port) / app(yproxy app id) / token(共享 token) / ca_cert(PEM，公开) /
--       insecure / is_default。token 是团队共享访问凭据（与内置 anon key 同信任级）；
--       ca_cert 是证书（公开物）。RLS：任意已认证用户可读，仅管理员可写。
--
-- 复用 cli schema 与 cli.is_admin()（ssh_keys.sql 已建）。幂等，可重复执行。

CREATE SCHEMA IF NOT EXISTS cli;

CREATE TABLE IF NOT EXISTS cli.proxy_hub (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  name        text        NOT NULL UNIQUE,           -- 实例名（如 proxyhub1）
  server      text        NOT NULL,                  -- host:port（hub 的 TLS 地址）
  app         text        NOT NULL,                  -- yproxy app id（如 infra）
  token       text        NOT NULL,                  -- 共享 token（团队访问凭据）
  ca_cert     text,                                  -- hub 自签证书 PEM（公开）
  insecure    boolean     NOT NULL DEFAULT false,    -- 跳过 TLS 校验（仅开发）
  is_default  boolean     NOT NULL DEFAULT false,    -- 默认 hub
  description text,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE cli.proxy_hub ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS proxy_hub_select_authenticated ON cli.proxy_hub;
CREATE POLICY proxy_hub_select_authenticated
  ON cli.proxy_hub
  FOR SELECT
  USING (auth.uid() IS NOT NULL);

DROP POLICY IF EXISTS proxy_hub_insert_admin ON cli.proxy_hub;
CREATE POLICY proxy_hub_insert_admin
  ON cli.proxy_hub FOR INSERT WITH CHECK (cli.is_admin());

DROP POLICY IF EXISTS proxy_hub_update_admin ON cli.proxy_hub;
CREATE POLICY proxy_hub_update_admin
  ON cli.proxy_hub FOR UPDATE USING (cli.is_admin()) WITH CHECK (cli.is_admin());

DROP POLICY IF EXISTS proxy_hub_delete_admin ON cli.proxy_hub;
CREATE POLICY proxy_hub_delete_admin
  ON cli.proxy_hub FOR DELETE USING (cli.is_admin());

DO $$
DECLARE
  r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['authenticated', 'service_role'] LOOP
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('GRANT USAGE ON SCHEMA cli TO %I', r);
      EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON cli.proxy_hub TO %I', r);
    END IF;
  END LOOP;
END
$$;
