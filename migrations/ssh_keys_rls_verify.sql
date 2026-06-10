-- ssh-key-provisioning：迁移与 RLS 行为验证（可重复 psql 脚本）
--
-- 目的：把 task 1.1 中临时验证过的 RLS 行为，固化为一个自包含、可重复运行、
--       任一断言不符即非零退出的验证产物。覆盖 requirements 7.2、8.1、8.2、8.3、8.4。
--
-- 运行：
--   psql -v ON_ERROR_STOP=1 -f ssh_keys_rls_verify.sql
-- 期望：脚本以退出码 0 结束，且打印 "ALL RLS ASSERTIONS PASSED"。
-- 任一负向用例意外成功、或任一正向用例失败，psql 立即以非零码退出。
--
-- 注意：本脚本不修改受测迁移 ssh_keys.sql。它先在“纯 Postgres”中补齐
--       Supabase 才有的依赖（auth schema / auth.uid() / pgcrypto），再 \i 加载真实迁移。
--       验证完毕回滚一切（DROP SCHEMA），不在数据库留痕。
--
-- RLS 仅对非超级用户、非表 owner 生效。脚本以建表者（默认运行用户）创建对象，
-- 再 SET ROLE 切到一个非特权角色 bk_rls_app 上执行用户级断言，从而让 RLS 真正生效。

\set ON_ERROR_STOP on
\echo '=== ssh_keys RLS verification: start ==='

-- ---------------------------------------------------------------------------
-- 0. 干净起点（可重复运行）：移除上一轮残留
-- ---------------------------------------------------------------------------
DROP SCHEMA IF EXISTS cli CASCADE;
DROP SCHEMA IF EXISTS auth CASCADE;
DROP ROLE IF EXISTS bk_rls_app;

-- ---------------------------------------------------------------------------
-- 1. 补齐 Supabase-only 依赖（不改受测迁移文件）
--    - pgcrypto：提供 gen_random_uuid()（迁移中 id 默认值依赖）。
--    - auth schema + auth.uid()：返回 GUC request.jwt.claim.sub::uuid，
--      模拟 Supabase 注入的当前会话用户身份。STABLE，可在策略/默认值中调用。
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA auth;

CREATE OR REPLACE FUNCTION auth.uid()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('request.jwt.claim.sub', true), '')::uuid;
$$;

-- 让被 SET ROLE 切换后的非特权角色也能解析 auth.uid()。
GRANT USAGE ON SCHEMA auth TO PUBLIC;

-- ---------------------------------------------------------------------------
-- 2. 加载真实迁移（自包含：脚本自己 \i 进来）
--    迁移内含 cli.ssh_keys 表 + admins + is_admin() + 4 条 RLS 策略 + 审计触发器。
-- ---------------------------------------------------------------------------
\echo '--- loading ssh_keys.sql (migration under test) ---'
\i ssh_keys.sql

-- ---------------------------------------------------------------------------
-- 3. 非特权应用角色（RLS 对其生效），并授予表权限
--    NOLOGIN 即可（用 SET ROLE 切换，不经登录）。
-- ---------------------------------------------------------------------------
CREATE ROLE bk_rls_app NOLOGIN;
GRANT USAGE ON SCHEMA cli TO bk_rls_app;
GRANT SELECT, INSERT, UPDATE ON cli.ssh_keys TO bk_rls_app;
-- is_admin() 是 SECURITY DEFINER，但执行需 EXECUTE 权限；admins 表本身无需直接授权。
GRANT EXECUTE ON FUNCTION cli.is_admin() TO bk_rls_app;
GRANT USAGE ON SCHEMA auth TO bk_rls_app;
GRANT EXECUTE ON FUNCTION auth.uid() TO bk_rls_app;

-- ---------------------------------------------------------------------------
-- 4. 测试身份固定 UUID
--    U1/U2：普通用户；A：管理员（写入 cli.admins）。
-- ---------------------------------------------------------------------------
\set U1 '11111111-1111-1111-1111-111111111111'
\set U2 '22222222-2222-2222-2222-222222222222'
\set A  '33333333-3333-3333-3333-333333333333'

-- 以建表者身份登记管理员名单（普通用户无权写 admins）。
INSERT INTO cli.admins (user_id) VALUES (:'A');

-- ===========================================================================
-- 断言辅助：在单个 DO 块里
--   - SET LOCAL ROLE bk_rls_app 让 RLS 生效；
--   - SET LOCAL request.jwt.claim.sub 设定 auth.uid()；
--   - 正向用例：直接执行，失败则 DO 抛错 → ON_ERROR_STOP 非零退出；
--   - 负向用例：包在 BEGIN/EXCEPTION 中，捕获到错误 = 预期；
--     若未抛错（即意外成功）则主动 RAISE EXCEPTION → 非零退出。
-- 每个 DO 块自带子事务语义；RESET ROLE 由块结束自动回收（SET LOCAL）。
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- T1 (8.1)：U1 INSERT 自己的行 status='pending' → 应成功
-- ---------------------------------------------------------------------------
\echo 'T1: U1 INSERT own pending row -> expect SUCCESS'
DO $$
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);

  INSERT INTO cli.ssh_keys (owner, name, host, public_key, fingerprint, status)
  VALUES (auth.uid(), 'bk-u1-host1', 'host1', 'ssh-ed25519 AAAAU1KEY', 'SHA256:u1', 'pending');

  IF (SELECT count(*) FROM cli.ssh_keys WHERE owner = auth.uid()) <> 1 THEN
    RAISE EXCEPTION 'T1 FAILED: expected 1 row visible to U1, got %',
      (SELECT count(*) FROM cli.ssh_keys WHERE owner = auth.uid());
  END IF;
  RAISE NOTICE 'T1 PASSED';
END $$;

-- ---------------------------------------------------------------------------
-- T2 (8.2)：U1 INSERT status='installed' → 应被 INSERT WITH CHECK 拒绝
-- ---------------------------------------------------------------------------
\echo 'T2: U1 INSERT status=installed -> expect REJECT (insert WITH CHECK)'
DO $$
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);
  BEGIN
    INSERT INTO cli.ssh_keys (owner, name, host, public_key, fingerprint, status)
    VALUES (auth.uid(), 'bk-u1-host2', 'host2', 'ssh-ed25519 AAAAU1KEY2', 'SHA256:u1b', 'installed');
    RAISE EXCEPTION 'T2 FAILED: INSERT with status=installed unexpectedly SUCCEEDED (self-promotion on insert not blocked)';
  EXCEPTION
    WHEN insufficient_privilege THEN
      RAISE NOTICE 'T2 PASSED: rejected with insufficient_privilege (RLS)';
  END;
END $$;

-- ---------------------------------------------------------------------------
-- T3 (8.2 owner isolation)：U1 INSERT owner=U2 → 应被拒
-- ---------------------------------------------------------------------------
\echo 'T3: U1 INSERT owner=U2 -> expect REJECT (owner isolation on insert)'
DO $$
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);
  BEGIN
    INSERT INTO cli.ssh_keys (owner, name, host, public_key, fingerprint, status)
    VALUES ('22222222-2222-2222-2222-222222222222', 'bk-spoof', 'host3', 'ssh-ed25519 SPOOF', 'SHA256:spoof', 'pending');
    RAISE EXCEPTION 'T3 FAILED: INSERT with owner=U2 unexpectedly SUCCEEDED (owner spoofing not blocked)';
  EXCEPTION
    WHEN insufficient_privilege THEN
      RAISE NOTICE 'T3 PASSED: rejected with insufficient_privilege (RLS)';
  END;
END $$;

-- ---------------------------------------------------------------------------
-- T4 (7.2 / 8.2 SELF-PROMOTION GUARD — 关键断言)：
--   U1 UPDATE 自己的行 SET status='installed' → 应被 owner-UPDATE WITH CHECK 拒绝
-- ---------------------------------------------------------------------------
\echo 'T4: U1 UPDATE own row SET status=installed -> expect REJECT (self-promotion guard)'
DO $$
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);
  BEGIN
    UPDATE cli.ssh_keys
       SET status = 'installed'
     WHERE owner = auth.uid() AND host = 'host1';
    RAISE EXCEPTION 'T4 FAILED: owner self-UPDATE to status=installed unexpectedly SUCCEEDED (PRIVILEGE ESCALATION)';
  EXCEPTION
    WHEN insufficient_privilege THEN
      RAISE NOTICE 'T4 PASSED: self-promotion rejected with insufficient_privilege (RLS)';
  END;
END $$;

-- ---------------------------------------------------------------------------
-- T5 (re-provision)：U1 UPDATE 自己的行、保持 status='pending'、换新公钥 → 应成功
-- ---------------------------------------------------------------------------
\echo 'T5: U1 UPDATE own row (new public_key, status stays pending) -> expect SUCCESS'
DO $$
DECLARE
  v_key text;
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);

  UPDATE cli.ssh_keys
     SET public_key = 'ssh-ed25519 AAAAU1ROTATED', fingerprint = 'SHA256:u1rot', status = 'pending'
   WHERE owner = auth.uid() AND host = 'host1';

  SELECT public_key INTO v_key
    FROM cli.ssh_keys WHERE owner = auth.uid() AND host = 'host1';
  IF v_key <> 'ssh-ed25519 AAAAU1ROTATED' THEN
    RAISE EXCEPTION 'T5 FAILED: re-provision UPDATE did not persist new public_key (got %)', v_key;
  END IF;
  RAISE NOTICE 'T5 PASSED';
END $$;

-- ---------------------------------------------------------------------------
-- T6 (8.2 owner isolation on read)：U2 SELECT → 看不到 U1 的任何行
-- ---------------------------------------------------------------------------
\echo 'T6: U2 SELECT -> expect 0 of U1 rows (owner read isolation)'
DO $$
DECLARE
  v_cnt int;
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '22222222-2222-2222-2222-222222222222', true);

  SELECT count(*) INTO v_cnt FROM cli.ssh_keys;  -- RLS 过滤后 U2 可见集合
  IF v_cnt <> 0 THEN
    RAISE EXCEPTION 'T6 FAILED: U2 can see % row(s); expected 0 (owner isolation leak)', v_cnt;
  END IF;
  RAISE NOTICE 'T6 PASSED';
END $$;

-- ---------------------------------------------------------------------------
-- T7 (7.2 / is_admin read-all)：管理员 A SELECT → 能看到 U1 的行
-- ---------------------------------------------------------------------------
\echo 'T7: admin A SELECT -> expect to SEE U1 row (is_admin read-all)'
DO $$
DECLARE
  v_cnt int;
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '33333333-3333-3333-3333-333333333333', true);

  SELECT count(*) INTO v_cnt
    FROM cli.ssh_keys
   WHERE owner = '11111111-1111-1111-1111-111111111111';
  IF v_cnt <> 1 THEN
    RAISE EXCEPTION 'T7 FAILED: admin A sees % of U1 rows; expected 1 (is_admin read-all broken)', v_cnt;
  END IF;
  RAISE NOTICE 'T7 PASSED';
END $$;

-- ---------------------------------------------------------------------------
-- T8 (7.2 admin state change)：管理员 A UPDATE U1 行 SET status='installed', installed_by=A → 应成功
-- ---------------------------------------------------------------------------
\echo 'T8: admin A UPDATE U1 row SET status=installed, installed_by=A -> expect SUCCESS'
DO $$
DECLARE
  v_status text;
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '33333333-3333-3333-3333-333333333333', true);

  UPDATE cli.ssh_keys
     SET status = 'installed',
         installed_by = auth.uid(),   -- 审计触发器要求 installed_by = auth.uid()
         installed_at = now()
   WHERE owner = '11111111-1111-1111-1111-111111111111' AND host = 'host1';

  SELECT status INTO v_status
    FROM cli.ssh_keys
   WHERE owner = '11111111-1111-1111-1111-111111111111' AND host = 'host1';
  IF v_status <> 'installed' THEN
    RAISE EXCEPTION 'T8 FAILED: admin state change did not persist; status=%', v_status;
  END IF;
  RAISE NOTICE 'T8 PASSED';
END $$;

-- ---------------------------------------------------------------------------
-- T9 (8.4 status CHECK constraint)：INSERT status='bogus' → 应被 CHECK 约束拒绝
--   关键：要单独验证 *CHECK 约束本身*（而非 RLS）。对非特权用户，RLS INSERT
--   WITH CHECK 要求 status='pending'，会先于 CHECK 约束以 insufficient_privilege
--   拒绝 'bogus'，从而掩盖 CHECK。故此处以表 owner 身份执行（RLS 被绕过），
--   此时只剩 CHECK 约束把关，能干净地证明 status CHECK 生效。
-- ---------------------------------------------------------------------------
\echo 'T9: INSERT status=bogus (as table owner, RLS bypassed) -> expect REJECT (CHECK constraint)'
DO $$
BEGIN
  -- 不切角色：以脚本运行者（表 owner）身份，RLS 不适用，仅 CHECK 约束生效。
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);
  BEGIN
    INSERT INTO cli.ssh_keys (owner, name, host, public_key, fingerprint, status)
    VALUES (auth.uid(), 'bk-u1-bogus', 'host9', 'ssh-ed25519 BOGUS', 'SHA256:bogus', 'bogus');
    RAISE EXCEPTION 'T9 FAILED: INSERT with status=bogus unexpectedly SUCCEEDED (CHECK constraint not enforced)';
  EXCEPTION
    WHEN check_violation THEN
      RAISE NOTICE 'T9 PASSED: rejected with check_violation (status CHECK enforced)';
  END;
END $$;

-- ---------------------------------------------------------------------------
-- T10 (8.3 幂等性)：再次 \i 加载同一迁移 → 应可无错重复执行（idempotent）
--   关键：此步在 T1–T9 之后、最终 DROP SCHEMA 之前执行，因此首轮 \i 创建的
--   schema/表/函数/策略/触发器此刻仍存在。再次加载迁移会走 IF NOT EXISTS /
--   CREATE OR REPLACE / DROP POLICY IF EXISTS / DROP TRIGGER IF EXISTS 这些
--   幂等路径。任何重复执行报错都会被 \set ON_ERROR_STOP on + -v ON_ERROR_STOP=1
--   捕获，psql 立即以非零码退出，无需额外守卫。
--   以表 owner（脚本运行者）身份重跑：T9 的 DO 块未切角色，顶层会话此刻即为
--   首轮 \i 所用的特权角色；RESET ROLE 仅作防御性确保 DDL 可执行。
-- ---------------------------------------------------------------------------
\echo 'T10: re-apply migration -> expect idempotent (no error)'
RESET ROLE;
\i ssh_keys.sql
\echo 'T10 PASSED: migration re-applied idempotently (no error)'

-- ---------------------------------------------------------------------------
-- 收尾：清理，保证可重复运行且不留痕
-- ---------------------------------------------------------------------------
DROP SCHEMA IF EXISTS cli CASCADE;
DROP SCHEMA IF EXISTS auth CASCADE;
DROP ROLE IF EXISTS bk_rls_app;

\echo '=== ALL RLS ASSERTIONS PASSED ==='
