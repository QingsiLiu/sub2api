-- Additive V2 contract and daily accounting identities. Historical lots, orders,
-- counters and allocation evidence are deliberately not rewritten.
CREATE TABLE IF NOT EXISTS subscription_contract_terms (
 term_id TEXT PRIMARY KEY,
 subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE CASCADE,
 starts_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS subscription_contract_terms_subscription ON subscription_contract_terms(subscription_id,starts_at);
CREATE TABLE IF NOT EXISTS subscription_contracts (
 subscription_id BIGINT PRIMARY KEY REFERENCES user_subscriptions(id) ON DELETE CASCADE,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 term_id TEXT NOT NULL REFERENCES subscription_contract_terms(term_id),
 revision BIGINT NOT NULL DEFAULT 1,
 mode TEXT NOT NULL CHECK(mode IN ('v2','legacy_daily')),
 kind TEXT NOT NULL DEFAULT '', plan_id BIGINT NOT NULL DEFAULT 0, plan_name TEXT NOT NULL DEFAULT '',
 unit_daily_usd NUMERIC(20,8) NOT NULL DEFAULT 0, quantity INTEGER NOT NULL DEFAULT 0,
 period_days INTEGER NOT NULL DEFAULT 0, starts_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
 is_current BOOLEAN NOT NULL DEFAULT FALSE,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS subscription_contracts_one_current_user ON subscription_contracts(user_id) WHERE is_current AND mode='v2';
CREATE TABLE IF NOT EXISTS subscription_daily_usage (
 subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE CASCADE,
 term_id TEXT NOT NULL REFERENCES subscription_contract_terms(term_id) ON DELETE CASCADE,
 usage_date DATE NOT NULL, used_usd NUMERIC(20,10) NOT NULL DEFAULT 0,
 PRIMARY KEY(subscription_id,term_id,usage_date)
);
CREATE TABLE IF NOT EXISTS subscription_ledger_state (
 subscription_id BIGINT PRIMARY KEY REFERENCES user_subscriptions(id) ON DELETE CASCADE,
 allocation_watermark BIGINT NOT NULL DEFAULT 0,
 baseline_date DATE NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS subscription_requests_subscription_id_id ON subscription_requests(subscription_id,id);
CREATE TABLE IF NOT EXISTS subscription_admission_cursor (
 subscription_id BIGINT PRIMARY KEY REFERENCES user_subscriptions(id) ON DELETE CASCADE,
 request_watermark BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS subscription_request_contracts (
 request_key VARCHAR PRIMARY KEY REFERENCES subscription_requests(request_key) ON DELETE CASCADE,
 term_id TEXT NOT NULL REFERENCES subscription_contract_terms(term_id), usage_date DATE NOT NULL
);
CREATE INDEX IF NOT EXISTS subscription_request_contracts_term ON subscription_request_contracts(term_id);
CREATE TABLE IF NOT EXISTS subscription_contract_changes (
 id BIGSERIAL PRIMARY KEY,
 subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE CASCADE,
 order_id BIGINT UNIQUE REFERENCES payment_orders(id),
 operation TEXT NOT NULL, before_contract JSONB, after_contract JSONB NOT NULL,
 before_lots JSONB NOT NULL, after_lots JSONB NOT NULL,
 lifetime_usage_baseline NUMERIC(20,10) NOT NULL DEFAULT 0,
 source_type TEXT NOT NULL, source_reference TEXT NOT NULL DEFAULT '', actor_id BIGINT NOT NULL DEFAULT 0,
 refund_status TEXT NOT NULL DEFAULT '', frozen_parent_status TEXT NOT NULL DEFAULT '',
 reversed_at TIMESTAMPTZ,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS subscription_contract_changes_subscription ON subscription_contract_changes(subscription_id,id);

-- A historical expired parent may already reference the upgrade's target plan.
-- Its identity and Key bindings must survive; live V2 uniqueness is by owner.
DROP INDEX IF EXISTS user_subscriptions_user_plan_unique_active;
CREATE INDEX IF NOT EXISTS user_subscriptions_user_plan_lookup ON user_subscriptions(user_id,plan_id) WHERE deleted_at IS NULL;

-- Lock each parent while capturing its usage baseline. Existing request writers
-- use the same parent lock, so a late allocation is either in this baseline or
-- above its watermark, never both. Per-user classification is conservative.
DO $$
DECLARE s RECORD; e RECORD; live_count INTEGER; lot_count INTEGER; kind_value TEXT;
        mode_value TEXT; days_value INTEGER; term_value TEXT; baseline NUMERIC; watermark BIGINT;
BEGIN
 FOR s IN SELECT * FROM user_subscriptions ORDER BY id FOR UPDATE LOOP
  IF EXISTS(SELECT 1 FROM subscription_contracts WHERE subscription_id=s.id) THEN CONTINUE; END IF;
  SELECT COUNT(*) INTO live_count FROM user_subscriptions WHERE user_id=s.user_id AND deleted_at IS NULL AND expires_at>NOW() AND status IN ('active','suspended');
  SELECT COUNT(*) INTO lot_count FROM user_subscription_entitlements WHERE user_subscription_id=s.id AND status='active' AND starts_at<=NOW() AND expires_at>NOW();
  SELECT x.*, p.name AS plan_name, p.validity_days, p.validity_unit, p.is_legacy_compat INTO e FROM user_subscription_entitlements x LEFT JOIN subscription_plans p ON p.id=COALESCE(x.plan_id,s.plan_id) WHERE x.user_subscription_id=s.id AND x.status='active' AND x.starts_at<=NOW() AND x.expires_at>NOW() ORDER BY x.id LIMIT 1;
  days_value:=CASE WHEN e.validity_unit IN ('week','weeks') THEN e.validity_days*7 WHEN e.validity_unit IN ('month','months') THEN e.validity_days*30 ELSE COALESCE(e.validity_days,0) END;
  kind_value:=CASE WHEN days_value=7 AND e.daily_limit_usd IN (90,180) THEN 'week' WHEN days_value=30 AND e.daily_limit_usd IN (45,90,180) THEN 'month' ELSE '' END;
  mode_value:=CASE WHEN live_count=1 AND lot_count=1 AND kind_value<>'' AND NOT COALESCE(e.is_legacy_compat,TRUE) AND s.status='active' AND s.deleted_at IS NULL THEN 'v2' ELSE 'legacy_daily' END;
  term_value:='legacy-'||s.id::TEXT;
  INSERT INTO subscription_contract_terms(term_id,subscription_id,starts_at,expires_at) VALUES(term_value,s.id,s.starts_at,s.expires_at) ON CONFLICT DO NOTHING;
  INSERT INTO subscription_contracts(subscription_id,user_id,term_id,mode,kind,plan_id,plan_name,unit_daily_usd,quantity,period_days,starts_at,expires_at,is_current)
  VALUES(s.id,s.user_id,term_value,mode_value,CASE WHEN mode_value='v2' THEN kind_value ELSE '' END,COALESCE(e.plan_id,s.plan_id,0),COALESCE(e.plan_name,''),CASE WHEN mode_value='v2' THEN e.daily_limit_usd ELSE 0 END,CASE WHEN mode_value='v2' THEN 1 ELSE 0 END,CASE WHEN mode_value='v2' THEN days_value ELSE 0 END,s.starts_at,CASE WHEN mode_value='v2' THEN e.expires_at ELSE s.expires_at END,mode_value='v2');
  SELECT COALESCE(SUM(daily_usage_usd) FILTER (WHERE (daily_window_start AT TIME ZONE 'Asia/Shanghai')::DATE=(NOW() AT TIME ZONE 'Asia/Shanghai')::DATE AND status NOT IN ('refunded','revoked')),0) INTO baseline FROM user_subscription_entitlements WHERE user_subscription_id=s.id;
  IF NOT EXISTS(SELECT 1 FROM user_subscription_entitlements WHERE user_subscription_id=s.id) AND (s.daily_window_start AT TIME ZONE 'Asia/Shanghai')::DATE=(NOW() AT TIME ZONE 'Asia/Shanghai')::DATE THEN baseline:=s.daily_usage_usd; END IF;
  SELECT COALESCE(MAX(a.id),0) INTO watermark FROM subscription_usage_allocations a JOIN subscription_requests r ON r.request_key=a.request_key WHERE r.subscription_id=s.id;
  INSERT INTO subscription_daily_usage(subscription_id,term_id,usage_date,used_usd) VALUES(s.id,term_value,(NOW() AT TIME ZONE 'Asia/Shanghai')::DATE,baseline) ON CONFLICT DO NOTHING;
  INSERT INTO subscription_ledger_state(subscription_id,allocation_watermark,baseline_date) VALUES(s.id,watermark,(NOW() AT TIME ZONE 'Asia/Shanghai')::DATE) ON CONFLICT DO NOTHING;
  INSERT INTO subscription_request_contracts(request_key,term_id,usage_date) SELECT request_key,term_value,(admitted_at AT TIME ZONE 'Asia/Shanghai')::DATE FROM subscription_requests WHERE subscription_id=s.id ON CONFLICT DO NOTHING;
  INSERT INTO subscription_admission_cursor(subscription_id,request_watermark) SELECT s.id,COALESCE(MAX(id),0) FROM subscription_requests WHERE subscription_id=s.id ON CONFLICT DO NOTHING;
 END LOOP;
END $$;
