-- secret-vault：blacksail.secrets 建表 + 唯一约束 + RLS 迁移
--
-- 数据放在应用域 schema `blacksail`（与 cli 工具专属的 cli schema 区分）：
-- secret-vault 是面向应用配置的密文存储，归属应用数据域，故沿用 `blacksail`。
--
-- 安全模型为「密文上云 + 主密钥留本机」：对称主密钥（AES-256-GCM）仅保存在本机
-- ~/.local/bk/vault.key（0600，永不离机），secret 经 AES-256-GCM 加密后以
-- base64(nonce||ciphertext) 形式写入本表的 value 列。本表 value 列恒为密文，
-- 绝不存明文；即便整库泄露，无本机主密钥亦无法解密；密文被篡改时解密因 GCM 认证失败而报错。
--
-- 全部 DDL 与策略幂等（schema/table IF NOT EXISTS、函数 CREATE OR REPLACE、
-- 策略 DROP POLICY IF EXISTS 后重建、触发器 DROP TRIGGER IF EXISTS 后重建），
-- 整个脚本可在 Supabase SQL 环境重复执行无错、不破坏既有数据。
--
-- 安全边界为纯 owner 隔离：无管理员、无状态机——每个用户仅能读写归属本人
-- （owner = auth.uid()）的 secret。owner 列默认绑定 auth.uid()，避免客户端伪造 owner。
--
-- ⚠️ PostgREST 暴露：bk 经 PostgREST 访问 `blacksail` schema 须在 Supabase 项目的
--    API 设置（Exposed schemas / db.schemas / PGRST_DB_SCHEMAS）中包含 `blacksail`，
--    否则 REST 请求会 404/PGRST106。本脚本已对 authenticated/service_role 授予
--    schema usage 与表权限；暴露 schema 属项目配置，迁移无法代劳。
--
-- 对应 requirements 8.1–8.5、7.3；design「Logical/Physical Data Model」「RLS」段。

-- ---------------------------------------------------------------------------
-- 应用域 schema：blacksail
-- ---------------------------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS blacksail;

-- ---------------------------------------------------------------------------
-- secrets：密文记录（owner 维度隔离）
--   value 列恒为密文 base64(nonce||ciphertext)（AES-256-GCM），绝不存明文。
--   unique(owner, app, key)：同一用户在同一 app 下同一 key 至多一条记录，
--   支撑 Store.Set 的 upsert(on_conflict=owner,app,key) 覆盖语义（R8.2, R1.3）。
--   owner 默认 auth.uid()：写入时自动绑定当前认证用户，客户端无法伪造 owner（R8.4）。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS blacksail.secrets (
  id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  owner      uuid        NOT NULL DEFAULT auth.uid(),
  app        text        NOT NULL,
  key        text        NOT NULL,
  value      text        NOT NULL,                       -- 密文 base64(nonce||ciphertext)，恒不含明文
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (owner, app, key)
);

COMMENT ON COLUMN blacksail.secrets.value IS
  '密文：base64(nonce||ciphertext)（AES-256-GCM）。本列仅存密文，绝不存明文（安全模型：主密钥留本机）。';

-- ---------------------------------------------------------------------------
-- updated_at 自动维护触发器（BEFORE UPDATE 将 updated_at 置为 now()）
--   使 store 层 upsert/update 时无需显式更新该列，审计时间戳始终一致。
--   幂等：CREATE OR REPLACE FUNCTION + DROP TRIGGER IF EXISTS 后重建。
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION blacksail.secrets_touch_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS secrets_touch_updated_at ON blacksail.secrets;
CREATE TRIGGER secrets_touch_updated_at
  BEFORE UPDATE ON blacksail.secrets
  FOR EACH ROW
  EXECUTE FUNCTION blacksail.secrets_touch_updated_at();

-- ---------------------------------------------------------------------------
-- 行级安全（RLS）—— 纯 owner 隔离（owner = auth.uid()）
--   每条策略均 owner=auth.uid()：用户仅能读/增/改/删归属本人的记录。
--   无管理员豁免、无状态机；这是 secret-vault 与 ssh_keys 的关键差异。
--   INSERT/UPDATE 的 WITH CHECK 阻止把行写成他人 owner（伪造 owner）。
--   策略幂等：DROP POLICY IF EXISTS 后重建。
-- ---------------------------------------------------------------------------
ALTER TABLE blacksail.secrets ENABLE ROW LEVEL SECURITY;

-- SELECT：仅能看到自己的记录（owner 读隔离，R8.3, R7.3）。
DROP POLICY IF EXISTS secrets_select_owner ON blacksail.secrets;
CREATE POLICY secrets_select_owner
  ON blacksail.secrets
  FOR SELECT
  USING (owner = auth.uid());

-- INSERT：仅能为自己登记，owner 必须等于 auth.uid()（防伪造 owner，R8.4）。
DROP POLICY IF EXISTS secrets_insert_owner ON blacksail.secrets;
CREATE POLICY secrets_insert_owner
  ON blacksail.secrets
  FOR INSERT
  WITH CHECK (owner = auth.uid());

-- UPDATE：仅能改自己的行，且改后 owner 仍须为本人（防把记录改归他人）。
DROP POLICY IF EXISTS secrets_update_owner ON blacksail.secrets;
CREATE POLICY secrets_update_owner
  ON blacksail.secrets
  FOR UPDATE
  USING (owner = auth.uid())
  WITH CHECK (owner = auth.uid());

-- DELETE：仅能删自己的行。
DROP POLICY IF EXISTS secrets_delete_owner ON blacksail.secrets;
CREATE POLICY secrets_delete_owner
  ON blacksail.secrets
  FOR DELETE
  USING (owner = auth.uid());

-- ---------------------------------------------------------------------------
-- 角色授权（Supabase 的 authenticated / service_role）
--   PostgREST 在 RLS 之上还需表级 GRANT 才能访问。用 role-existence 守卫，
--   使脚本在没有这些角色的 vanilla Postgres（RLS 验证脚本）下也能幂等执行而不报错。
--   RLS 仍是真正的行级访问控制；GRANT 只是表级前置许可。
-- ---------------------------------------------------------------------------
DO $$
DECLARE
  r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['authenticated', 'service_role'] LOOP
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
      EXECUTE format('GRANT USAGE ON SCHEMA blacksail TO %I', r);
      EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON blacksail.secrets TO %I', r);
    END IF;
  END LOOP;
END
$$;
