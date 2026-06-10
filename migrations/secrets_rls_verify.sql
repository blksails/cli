-- secret-vault：迁移与 RLS 行为验证（可重复 psql 脚本）
--
-- 目的：把 task 1.1 的 RLS 行为固化为自包含、可重复运行、任一断言不符即非零退出的
--       验证产物。覆盖 requirements 8.1、8.2、8.3、8.4、8.5、7.3。
--
-- 运行：
--   psql -v ON_ERROR_STOP=1 -f secrets_rls_verify.sql
-- 期望：脚本以退出码 0 结束，且打印 "ALL RLS ASSERTIONS PASSED"。
-- 任一负向用例意外成功、或任一正向用例失败，psql 立即以非零码退出。
--
-- 注意：本脚本不修改受测迁移 secrets.sql。它先在“纯 Postgres”中补齐 Supabase 才有的
--       依赖（auth schema / auth.uid() / pgcrypto），再 \i 加载真实迁移。
--       验证完毕回滚一切（DROP SCHEMA），不在数据库留痕。
--
-- RLS 仅对非超级用户、非表 owner 生效。脚本以建表者（默认运行用户）创建对象，
-- 再 SET LOCAL ROLE 切到非特权角色 bk_rls_app 上执行用户级断言，从而让 RLS 真正生效。

\set ON_ERROR_STOP on
\echo '=== secrets RLS verification: start ==='

-- ---------------------------------------------------------------------------
-- 0. 干净起点（可重复运行）：移除上一轮残留
-- ---------------------------------------------------------------------------
DROP SCHEMA IF EXISTS blacksail CASCADE;
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
--    迁移内含 blacksail.secrets 表 + unique(owner,app,key) + 4 条 owner RLS 策略
--    + updated_at 自动维护触发器。
-- ---------------------------------------------------------------------------
\echo '--- loading secrets.sql (migration under test) ---'
\i secrets.sql

-- ---------------------------------------------------------------------------
-- 3. 非特权应用角色（RLS 对其生效），并授予表权限
--    NOLOGIN 即可（用 SET ROLE 切换，不经登录）。
-- ---------------------------------------------------------------------------
CREATE ROLE bk_rls_app NOLOGIN;
GRANT USAGE ON SCHEMA blacksail TO bk_rls_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON blacksail.secrets TO bk_rls_app;
GRANT USAGE ON SCHEMA auth TO bk_rls_app;
GRANT EXECUTE ON FUNCTION auth.uid() TO bk_rls_app;

-- ---------------------------------------------------------------------------
-- 4. 测试身份固定 UUID（U1/U2：两个普通用户）
-- ---------------------------------------------------------------------------
\set U1 '11111111-1111-1111-1111-111111111111'
\set U2 '22222222-2222-2222-2222-222222222222'

-- ===========================================================================
-- 断言辅助约定：
--   - SET LOCAL ROLE bk_rls_app 让 RLS 生效；
--   - SET LOCAL request.jwt.claim.sub 设定 auth.uid()；
--   - 正向用例：直接执行，失败则 DO 抛错 → ON_ERROR_STOP 非零退出；
--   - 负向用例：包在 BEGIN/EXCEPTION 中，捕获到错误 = 预期；
--     若未抛错（意外成功）则主动 RAISE EXCEPTION → 非零退出。
-- 每个 DO 块自带子事务语义；SET LOCAL ROLE 由块结束自动回收。
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- T1 (8.1 / 8.4)：U1 INSERT 自己的密文行（owner=auth.uid()）→ 应成功
-- ---------------------------------------------------------------------------
\echo 'T1: U1 INSERT own ciphertext row -> expect SUCCESS'
DO $$
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);

  INSERT INTO blacksail.secrets (owner, app, key, value)
  VALUES (auth.uid(), 'app1', 'DB_URL', 'base64-ciphertext-u1-dburl');

  IF (SELECT count(*) FROM blacksail.secrets WHERE owner = auth.uid()) <> 1 THEN
    RAISE EXCEPTION 'T1 FAILED: expected 1 row visible to U1, got %',
      (SELECT count(*) FROM blacksail.secrets WHERE owner = auth.uid());
  END IF;
  RAISE NOTICE 'T1 PASSED';
END $$;

-- ---------------------------------------------------------------------------
-- T2 (8.4 owner spoof guard)：U1 INSERT owner=U2 → 应被 INSERT WITH CHECK 拒绝
-- ---------------------------------------------------------------------------
\echo 'T2: U1 INSERT owner=U2 (spoof) -> expect REJECT (insert WITH CHECK owner=auth.uid())'
DO $$
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);
  BEGIN
    INSERT INTO blacksail.secrets (owner, app, key, value)
    VALUES ('22222222-2222-2222-2222-222222222222', 'app1', 'SPOOF', 'base64-ciphertext-spoof');
    RAISE EXCEPTION 'T2 FAILED: INSERT with owner=U2 unexpectedly SUCCEEDED (owner spoofing not blocked)';
  EXCEPTION
    WHEN insufficient_privilege THEN
      RAISE NOTICE 'T2 PASSED: rejected with insufficient_privilege (RLS)';
  END;
END $$;

-- ---------------------------------------------------------------------------
-- T3 (8.3 / 7.3 owner read isolation — 关键断言)：
--   U2 SELECT → 看不到 U1 的任何行
-- ---------------------------------------------------------------------------
\echo 'T3: U2 SELECT -> expect 0 of U1 rows (owner read isolation)'
DO $$
DECLARE
  v_cnt int;
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '22222222-2222-2222-2222-222222222222', true);

  SELECT count(*) INTO v_cnt FROM blacksail.secrets;  -- RLS 过滤后 U2 可见集合
  IF v_cnt <> 0 THEN
    RAISE EXCEPTION 'T3 FAILED: U2 can see % row(s); expected 0 (owner isolation leak)', v_cnt;
  END IF;
  RAISE NOTICE 'T3 PASSED';
END $$;

-- ---------------------------------------------------------------------------
-- T4 (8.3 owner UPDATE)：U1 UPDATE 自己的行换新密文 → 应成功，且 U1 SELECT 见新值
-- ---------------------------------------------------------------------------
\echo 'T4: U1 UPDATE own row value -> expect SUCCESS; U1 sees new value'
DO $$
DECLARE
  v_val text;
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);

  UPDATE blacksail.secrets
     SET value = 'base64-ciphertext-u1-dburl-ROTATED'
   WHERE owner = auth.uid() AND app = 'app1' AND key = 'DB_URL';

  SELECT value INTO v_val
    FROM blacksail.secrets
   WHERE owner = auth.uid() AND app = 'app1' AND key = 'DB_URL';
  IF v_val <> 'base64-ciphertext-u1-dburl-ROTATED' THEN
    RAISE EXCEPTION 'T4 FAILED: UPDATE did not persist new value (got %)', v_val;
  END IF;
  RAISE NOTICE 'T4 PASSED';
END $$;

-- ---------------------------------------------------------------------------
-- T5 (8.2 unique(owner,app,key))：U1 INSERT 同 (app,key) 重复行 → 应被唯一约束拒绝
--   证明同一 (owner,app,key) 至多一条，Store.Set 必须走 upsert 而非裸 insert。
-- ---------------------------------------------------------------------------
\echo 'T5: U1 INSERT duplicate (app1,DB_URL) -> expect REJECT (unique constraint)'
DO $$
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);
  BEGIN
    INSERT INTO blacksail.secrets (owner, app, key, value)
    VALUES (auth.uid(), 'app1', 'DB_URL', 'base64-ciphertext-dup');
    RAISE EXCEPTION 'T5 FAILED: duplicate (owner,app,key) INSERT unexpectedly SUCCEEDED (unique constraint missing)';
  EXCEPTION
    WHEN unique_violation THEN
      RAISE NOTICE 'T5 PASSED: rejected with unique_violation (unique(owner,app,key) enforced)';
  END;
END $$;

-- ---------------------------------------------------------------------------
-- T6 (8.3 owner DELETE)：U1 再插一行，DELETE 其中一行 → 应成功且只删目标
-- ---------------------------------------------------------------------------
\echo 'T6: U1 DELETE one of own rows -> expect SUCCESS; only target removed'
DO $$
DECLARE
  v_cnt int;
BEGIN
  SET LOCAL ROLE bk_rls_app;
  PERFORM set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);

  -- 先插入第二行（不同 key），确保 DELETE 后仍剩一行可校验“只删目标”。
  INSERT INTO blacksail.secrets (owner, app, key, value)
  VALUES (auth.uid(), 'app1', 'API_KEY', 'base64-ciphertext-u1-apikey');

  DELETE FROM blacksail.secrets
   WHERE owner = auth.uid() AND app = 'app1' AND key = 'DB_URL';

  SELECT count(*) INTO v_cnt FROM blacksail.secrets WHERE owner = auth.uid();
  IF v_cnt <> 1 THEN
    RAISE EXCEPTION 'T6 FAILED: after delete expected 1 remaining U1 row, got %', v_cnt;
  END IF;
  IF EXISTS (SELECT 1 FROM blacksail.secrets WHERE owner = auth.uid() AND key = 'DB_URL') THEN
    RAISE EXCEPTION 'T6 FAILED: deleted target row (DB_URL) still present';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM blacksail.secrets WHERE owner = auth.uid() AND key = 'API_KEY') THEN
    RAISE EXCEPTION 'T6 FAILED: non-target row (API_KEY) was wrongly removed';
  END IF;
  RAISE NOTICE 'T6 PASSED';
END $$;

-- ---------------------------------------------------------------------------
-- T7 (8.5 幂等性 + 数据不丢)：再次 \i 加载同一迁移 → 应可无错重复执行，
--   且已有数据（T6 后 U1 的 1 行）保持不变。
--   关键：此步在 T1–T6 之后、最终 DROP SCHEMA 之前执行，首轮 \i 创建的
--   schema/表/约束/策略/触发器此刻仍存在。再次加载迁移会走 IF NOT EXISTS /
--   CREATE OR REPLACE / DROP POLICY IF EXISTS / DROP TRIGGER IF EXISTS 幂等路径。
--   任何重复执行报错都会被 ON_ERROR_STOP 捕获，psql 立即非零退出。
--   以表 owner（脚本运行者）身份重跑：RESET ROLE 确保 DDL 可执行。
-- ---------------------------------------------------------------------------
\echo 'T7: re-apply migration -> expect idempotent (no error) AND data preserved'
RESET ROLE;
\i secrets.sql

-- 重新加载后，以 owner 身份（RLS 不适用于表 owner）确认 U1 数据仍在、未丢失。
DO $$
DECLARE
  v_cnt int;
BEGIN
  SELECT count(*) INTO v_cnt
    FROM blacksail.secrets
   WHERE owner = '11111111-1111-1111-1111-111111111111';
  IF v_cnt <> 1 THEN
    RAISE EXCEPTION 'T7 FAILED: after re-apply expected 1 preserved U1 row, got % (data loss on re-run)', v_cnt;
  END IF;
  RAISE NOTICE 'T7 PASSED: migration re-applied idempotently, existing data preserved';
END $$;

-- ---------------------------------------------------------------------------
-- 收尾：清理，保证可重复运行且不留痕
-- ---------------------------------------------------------------------------
DROP SCHEMA IF EXISTS blacksail CASCADE;
DROP SCHEMA IF EXISTS auth CASCADE;
DROP ROLE IF EXISTS bk_rls_app;

\echo '=== ALL RLS ASSERTIONS PASSED ==='
